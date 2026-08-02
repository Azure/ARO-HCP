// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package nodehealth

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"
)

const (
	// resyncInterval is how often every node is re-enqueued for evaluation even
	// without new watch events, so a firing that becomes true purely because the
	// dwell elapsed (or a recovery that clears) is reconciled, and the
	// wedged-nodes gauge is refreshed.
	resyncInterval = 30 * time.Second

	// podByNodeIndex indexes Pods by the node they are scheduled to.
	podByNodeIndex = "nodehealth-pod-by-node"
	// eventBySourceHostIndex indexes Events by Event.Source.Host, the node whose
	// kubelet emitted them. Keying on the emitting node instead of the involved
	// Pod is deletion-safe: a node's failure Events are one lookup and the index
	// does not resolve through a Pod that may already be gone.
	eventBySourceHostIndex = "nodehealth-event-by-source-host"
)

// Controller is the level-driven controller that ties detection (the pure
// detectors.Decide function, fed from shared informers) to a label/unlabel
// reconcile of wedged nodes. Its two operational switches are hot-reloadable
// via SetConfig.
type Controller struct {
	nodeLister   corelisters.NodeLister
	podIndexer   cache.Indexer
	eventIndexer cache.Indexer
	hasSynced    []cache.InformerSynced

	// obsMu guards observedSince and successAt, which are read by the workers and
	// written by the pod handlers and the config-informer thread (on a
	// disabled->enabled transition).
	obsMu sync.Mutex
	// observedSince is the clock time the controller began observing: cache sync
	// on start, or a disabled->enabled transition. Detection treats the success
	// signal as indeterminate until a full window has elapsed since then, so a
	// cold view after a restart or a hot enable is never misread as no-success.
	observedSince time.Time
	// successAt records, per node name, the most recent time the controller saw a
	// non-host-network PodReadyToStartContainers=True transition. It is advanced
	// from pod add/update/delete so a success survives its short-lived pod being
	// deleted, and is pruned by window when read. It is cleared whenever
	// observation (re)starts.
	successAt map[string]time.Time

	labeler *labeler
	clock   func() time.Time

	workqueue workqueue.TypedRateLimitingInterface[string]
	config    atomic.Pointer[Config]
}

// NewController constructs a Controller and wires the Node, Pod, and Event
// informers with the indexers the pure detectors.Decide function reads. If clock is nil,
// time.Now is used.
func NewController(
	kubeClientset kubernetes.Interface,
	nodeInformer coreinformers.NodeInformer,
	podInformer coreinformers.PodInformer,
	eventInformer coreinformers.EventInformer,
	recorder record.EventRecorder,
	clock func() time.Time,
	initial Config,
) (*Controller, error) {
	if clock == nil {
		clock = time.Now
	}
	if err := initial.Validate(); err != nil {
		return nil, fmt.Errorf("invalid initial node-health config: %w", err)
	}

	if err := podInformer.Informer().AddIndexers(cache.Indexers{podByNodeIndex: podByNodeIndexFunc}); err != nil {
		return nil, fmt.Errorf("failed to add pod-by-node indexer: %w", err)
	}
	if err := eventInformer.Informer().AddIndexers(cache.Indexers{eventBySourceHostIndex: eventBySourceHostIndexFunc}); err != nil {
		return nil, fmt.Errorf("failed to add event-by-source-host indexer: %w", err)
	}

	c := &Controller{
		nodeLister:   nodeInformer.Lister(),
		podIndexer:   podInformer.Informer().GetIndexer(),
		eventIndexer: eventInformer.Informer().GetIndexer(),
		hasSynced: []cache.InformerSynced{
			nodeInformer.Informer().HasSynced,
			podInformer.Informer().HasSynced,
			eventInformer.Informer().HasSynced,
		},
		labeler:   newLabeler(kubeClientset, recorder, clock),
		clock:     clock,
		successAt: make(map[string]time.Time),
		workqueue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{Name: ControllerName},
		),
	}
	c.SetConfig(initial)

	if _, err := nodeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.enqueueNodeObject(obj) },
		UpdateFunc: func(_, newObj interface{}) { c.enqueueNodeObject(newObj) },
	}); err != nil {
		return nil, fmt.Errorf("failed to add node event handler: %w", err)
	}
	if _, err := podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.recordPodSuccess(obj); c.enqueuePodObject(obj) },
		UpdateFunc: func(_, newObj interface{}) { c.recordPodSuccess(newObj); c.enqueuePodObject(newObj) },
		DeleteFunc: func(obj interface{}) { c.recordPodSuccess(obj); c.enqueuePodObject(obj) },
	}); err != nil {
		return nil, fmt.Errorf("failed to add pod event handler: %w", err)
	}
	if _, err := eventInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.enqueueEventObject(obj) },
		UpdateFunc: func(_, newObj interface{}) { c.enqueueEventObject(newObj) },
	}); err != nil {
		return nil, fmt.Errorf("failed to add kubelet-event handler: %w", err)
	}

	return c, nil
}

// Config returns a copy of the current configuration snapshot.
func (c *Controller) Config() Config { return *c.config.Load() }

// SetConfig atomically replaces the configuration snapshot.
func (c *Controller) SetConfig(cfg Config) {
	c.config.Store(&cfg)
}

// OnConfigMap parses config from the named key of a ConfigMap and applies it.
// A parse error is logged and the previous config is retained.
func (c *Controller) OnConfigMap(cm *corev1.ConfigMap, key string) {
	data, ok := cm.Data[key]
	if !ok {
		klog.InfoS("node-health config key not found in ConfigMap, keeping current config",
			"configmap", cm.Name, "key", key)
		return
	}
	cfg, err := Parse([]byte(data))
	if err != nil {
		klog.ErrorS(err, "invalid node-health config, keeping current config", "configmap", cm.Name)
		return
	}
	klog.InfoS("node-health config reloaded", "configmap", cm.Name, "enabled", cfg.Enabled)
	wasEnabled := c.config.Load().Enabled
	c.SetConfig(cfg)
	// A disabled->enabled transition restarts observation: the success map is
	// discarded and the observedSince warm-up begins now, so the success signal
	// is treated as indeterminate until a full window has been watched under the
	// enabled controller.
	if cfg.Enabled && !wasEnabled {
		c.beginObserving(c.clock())
	}
	// A hot enabled->disabled flip stops all reconciles, so the gauge would
	// otherwise freeze at its last value. Zero it immediately so a disabled
	// controller reads zero wedged nodes rather than a stale count.
	if !cfg.Enabled && wasEnabled {
		wedgedNodes.Set(0)
		nodeWedged.Reset()
	}
}

// OnConfigMapDeleted reverts to the safe built-in default (disabled) when the
// config ConfigMap is removed, so rollback via ConfigMap deletion is not
// surprising for operators.
func (c *Controller) OnConfigMapDeleted() {
	klog.InfoS("node-health ConfigMap deleted, reverting to disabled default config")
	c.SetConfig(Default())
	// Reverting to the disabled default stops all reconciles, so zero the gauge
	// rather than leave it frozen at its last count.
	wedgedNodes.Set(0)
	nodeWedged.Reset()
}

func (c *Controller) enqueue(node string) {
	if node == "" || !c.config.Load().Enabled {
		return
	}
	c.workqueue.Add(node)
}

func (c *Controller) enqueueNodeObject(obj interface{}) {
	if name, ok := nodeName(obj); ok {
		c.enqueue(name)
	}
}

func (c *Controller) enqueuePodObject(obj interface{}) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	if pod, ok := obj.(*corev1.Pod); ok {
		c.enqueue(pod.Spec.NodeName)
	}
}

func (c *Controller) enqueueEventObject(obj interface{}) {
	ev, ok := obj.(*corev1.Event)
	if !ok {
		return
	}
	// Enqueue the node whose kubelet emitted the Event. Reading Source.Host
	// directly is deletion-safe: it does not depend on the involved Pod still
	// being cached.
	if ev.Source.Host != "" {
		c.enqueue(ev.Source.Host)
	}
}

// beginObserving (re)starts the observation warm-up: it stamps observedSince to
// now and clears any recorded success history so a fresh full window must be
// watched before the success signal is trusted. Called after cache sync in Run
// and on a disabled->enabled transition.
func (c *Controller) beginObserving(now time.Time) {
	c.obsMu.Lock()
	defer c.obsMu.Unlock()
	c.observedSince = now
	c.successAt = make(map[string]time.Time)
}

// recordPodSuccess advances the per-node success timestamp when a pod shows a
// fresh sandbox (non-host-network PodReadyToStartContainers=True). It is called
// from the pod add/update/delete handlers, so a success is captured from the
// pod's last-seen state even as the short-lived pod is being deleted. A disabled
// controller records nothing: beginObserving discards the map on startup and on
// every disabled->enabled transition, so anything recorded while off could never
// be read, and keeping it would grow the map for every node name ever seen.
func (c *Controller) recordPodSuccess(obj interface{}) {
	if !c.config.Load().Enabled {
		return
	}
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	pod, ok := obj.(*corev1.Pod)
	if !ok || pod.Spec.NodeName == "" {
		return
	}
	at, ok := detectors.SuccessAt(pod)
	if !ok {
		return
	}
	c.obsMu.Lock()
	defer c.obsMu.Unlock()
	if prev, exists := c.successAt[pod.Spec.NodeName]; !exists || at.After(prev) {
		c.successAt[pod.Spec.NodeName] = at
	}
}

// observation returns the node's warm-up start and its most recent recorded
// success, pruning any success older than the widest detector window so the map
// does not grow without bound.
func (c *Controller) observation(node string) (observedSince, lastSuccessAt time.Time) {
	c.obsMu.Lock()
	defer c.obsMu.Unlock()
	observedSince = c.observedSince
	at, ok := c.successAt[node]
	if !ok {
		return observedSince, time.Time{}
	}
	// Measured in absolute terms so a future-dated timestamp (kubelet clock skew)
	// is pruned rather than retained forever by a negative age.
	if c.clock().Sub(at).Abs() >= detectors.MaxWindow() {
		delete(c.successAt, node)
		return observedSince, time.Time{}
	}
	return observedSince, at
}

// Run starts the controller workers and blocks until the context is cancelled.
func (c *Controller) Run(ctx context.Context, workers int) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	logger := klog.FromContext(ctx)
	logger.Info("Starting node-health controller")

	logger.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(ctx.Done(), c.hasSynced...); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}
	// Record when the controller begins observing. Until a full detector window
	// has elapsed past this point, detection treats the success signal as
	// indeterminate so a cold view is never misread as no-success. This also
	// clears any success history carried across the cache resync.
	c.beginObserving(c.clock())

	logger.Info("Starting workers", "count", workers)
	for i := 0; i < workers; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}
	go wait.UntilWithContext(ctx, c.resyncAll, resyncInterval)

	logger.Info("Node-health controller started")
	<-ctx.Done()
	logger.Info("Shutting down node-health controller")
	return nil
}

// resyncAll re-enqueues every candidate node so time-based firings and
// recoveries are reconciled without new Events, and refreshes the wedged-nodes
// gauge.
//
// The sweep is the union of two cheap lister selectors rather than every node in
// the cluster. SWIFT-v2 nodes are the only nodes a detector can apply to, so they
// are the detection candidates; wedged-labeled nodes are added so a label left
// on a node that has since stopped being a detection candidate is still swept and
// retired. Relying on the node watch alone for that cleanup would miss any
// transition that happened while the controller was down.
func (c *Controller) resyncAll(ctx context.Context) {
	if !c.config.Load().Enabled {
		wedgedNodes.Set(0)
		nodeWedged.Reset()
		return
	}
	logger := klog.FromContext(ctx)

	swiftSelector := labels.SelectorFromSet(labels.Set{detectors.SwiftV2LabelKey: detectors.SwiftV2LabelValue})
	swiftNodes, err := c.nodeLister.List(swiftSelector)
	if err != nil {
		logger.Error(err, "resync: failed to list SWIFT nodes")
		return
	}

	wedgedNodeList, err := c.listWedgedNodes()
	if err != nil {
		logger.Error(err, "resync: failed to list wedged nodes")
		return
	}
	wedgedNodes.Set(float64(len(wedgedNodeList)))
	// Rebuilt from the authoritative list rather than mutated per transition, so
	// every way a node can stop being wedged converges within one resync: it
	// recovered, its label was removed out of band, it stopped being a detection
	// candidate, it was deleted, or the controller restarted and lost its memory.
	nodeWedged.Reset()
	for _, n := range wedgedNodeList {
		nodeWedged.WithLabelValues(
			n.Name,
			n.Annotations[annotationDetector],
			n.Annotations[annotationSignature],
		).Set(1)
	}

	// The two selectors overlap on the common case (a wedged SWIFT node), so
	// dedupe before enqueuing. The workqueue would coalesce anyway; deduping here
	// keeps the sweep's cost proportional to distinct nodes.
	seen := make(map[string]struct{}, len(swiftNodes)+len(wedgedNodeList))
	for _, n := range swiftNodes {
		seen[n.Name] = struct{}{}
	}
	for _, n := range wedgedNodeList {
		seen[n.Name] = struct{}{}
	}
	for name := range seen {
		c.enqueue(name)
	}
}

// listWedgedNodes returns the nodes currently carrying the wedged health label.
// It selects on the wedged label alone rather than intersecting with the SWIFT-v2
// selector, so a node that still carries the label after losing its SWIFT label
// is still found rather than silently dropped from the gauge and the sweep.
func (c *Controller) listWedgedNodes() ([]*corev1.Node, error) {
	return c.nodeLister.List(labels.SelectorFromSet(labels.Set{labelKey: labelValue}))
}

// countWedgedNodes returns how many nodes currently carry the wedged health
// label.
func (c *Controller) countWedgedNodes() (int, error) {
	nodes, err := c.listWedgedNodes()
	if err != nil {
		return 0, err
	}
	return len(nodes), nil
}

func (c *Controller) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *Controller) processNextWorkItem(ctx context.Context) bool {
	key, shutdown := c.workqueue.Get()
	if shutdown {
		return false
	}
	defer c.workqueue.Done(key)

	if err := c.syncHandler(ctx, key); err != nil {
		utilruntime.HandleError(fmt.Errorf("error reconciling node %q: %w", key, err))
		c.workqueue.AddRateLimited(key)
		return true
	}
	c.workqueue.Forget(key)
	return true
}

// syncHandler reconciles a single node's health label to match the pure
// detectors.Decide verdict computed from the informers' current view of that
// node.
func (c *Controller) syncHandler(ctx context.Context, name string) error {
	logger := klog.FromContext(ctx).WithValues("node", name)
	cfg := c.config.Load()
	if !cfg.Enabled {
		return nil
	}

	node, err := c.nodeLister.Get(name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get node: %w", err)
	}

	// Answer the ownership question before paying for the node's Pods and Events.
	// Every Pod and kubelet Event enqueues the node it belongs to, so on a
	// management cluster most reconciles are for nodes no detector owns. Decide
	// applies the same gate, so this reaches the same verdict without the scan.
	if !detectors.AnyApplies(node) {
		return c.retireStaleLabel(ctx, node, logger)
	}

	events, pods, err := c.gather(name)
	if err != nil {
		return err
	}

	observedSince, lastSuccessAt := c.observation(name)
	decision, snap := detectors.Decide(node, events, pods, c.clock(), observedSince, lastSuccessAt)
	switch decision {
	case detectors.DecisionWedged:
		changed, err := c.labeler.label(ctx, node, snap.DetectorName, snap)
		if err != nil {
			return err
		}
		if changed {
			detectionsTotal.WithLabelValues(snap.DetectorName, snap.MatchedSignature).Inc()
			logger.Info("detector fired; node labeled wedged",
				"detector", snap.DetectorName,
				"signature", snap.MatchedSignature,
				"failures", snap.FailureCount,
				"recentSuccess", snap.RecentSuccess)
		}
		// Mitigation of a labeled node (cordon, taint, evict, delete) is owned
		// by a separate controller; this controller stops at labeling.
	case detectors.DecisionHealthy:
		if _, err := c.labeler.unlabel(ctx, node); err != nil {
			return err
		}
	case detectors.DecisionNotApplicable:
		// Reached only if Decide's ownership gate disagrees with the AnyApplies
		// short-circuit above, which it cannot for the same node. Kept so the
		// switch covers Decide's full contract rather than relying on the caller.
		return c.retireStaleLabel(ctx, node, logger)
	case detectors.DecisionUnknown:
		logger.V(5).Info("insufficient evidence; leaving label unchanged")
	}
	return nil
}

// retireStaleLabel drops the wedged label from a node no detector owns. Such a
// label can only be one this controller left behind, for example when the node
// stopped being a SWIFT-v2 node while labeled. unlabel is a no-op when the label
// is absent or carries a foreign value.
func (c *Controller) retireStaleLabel(ctx context.Context, node *corev1.Node, logger klog.Logger) error {
	changed, err := c.labeler.unlabel(ctx, node)
	if err != nil {
		return err
	}
	if changed {
		logger.Info("retired stale wedged label; no detector applies to this node")
	}
	return nil
}

// gather returns the Events and Pods the informers currently hold for a node:
// the Pods scheduled to it, and the Events its kubelet emitted (keyed by
// Event.Source.Host). Reading Events by emitting node rather than by involved Pod
// keeps the failure evidence available even after the Pod is deleted.
func (c *Controller) gather(nodeName string) ([]*corev1.Event, []*corev1.Pod, error) {
	podObjs, err := c.podIndexer.ByIndex(podByNodeIndex, nodeName)
	if err != nil {
		return nil, nil, fmt.Errorf("list pods for node: %w", err)
	}
	pods := make([]*corev1.Pod, 0, len(podObjs))
	for _, o := range podObjs {
		if pod, ok := o.(*corev1.Pod); ok {
			pods = append(pods, pod)
		}
	}

	evObjs, err := c.eventIndexer.ByIndex(eventBySourceHostIndex, nodeName)
	if err != nil {
		return nil, nil, fmt.Errorf("list events for node: %w", err)
	}
	events := make([]*corev1.Event, 0, len(evObjs))
	for _, eo := range evObjs {
		if ev, ok := eo.(*corev1.Event); ok {
			events = append(events, ev)
		}
	}
	return events, pods, nil
}

func podByNodeIndexFunc(obj interface{}) ([]string, error) {
	pod, ok := obj.(*corev1.Pod)
	if !ok || pod.Spec.NodeName == "" {
		return nil, nil
	}
	return []string{pod.Spec.NodeName}, nil
}

func eventBySourceHostIndexFunc(obj interface{}) ([]string, error) {
	ev, ok := obj.(*corev1.Event)
	if !ok || ev.Source.Host == "" {
		return nil, nil
	}
	return []string{ev.Source.Host}, nil
}

func nodeName(obj interface{}) (string, bool) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	node, ok := obj.(*corev1.Node)
	if !ok {
		return "", false
	}
	return node.Name, true
}
