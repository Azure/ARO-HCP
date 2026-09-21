// Copyright 2026 Microsoft Corporation
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

package nodemitigation

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/dynamic"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	"github.com/Azure/ARO-HCP/internal/utils"
	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"
	clientset "github.com/Azure/ARO-HCP/mgmt-agent/pkg/generated/clientset/versioned"
)

var (
	ErrPaused = errors.New("mitigation writes paused by mode or configuration change")
	mtpncGVR  = schema.GroupVersionResource{Group: "multitenancy.acn.azure.com", Version: "v1alpha1", Resource: "multitenantpodnetworkconfigs"}
)

const (
	budgetName = "node-mitigation"
	maxRecords = 1024
)

type clusterKey struct{}

func (clusterKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues("controller", ControllerName)
}

type Controller struct {
	kube                 kubernetes.Interface
	records              clientset.Interface
	dynamic              dynamic.Interface
	azure                AzureReader
	namespace            string
	clock                func() time.Time
	observer             string
	routes               map[string]Mitigator
	queue                workqueue.TypedRateLimitingInterface[clusterKey]
	synced               []cache.InformerSynced
	mu                   sync.RWMutex
	config               Config
	revision             uint64
	configurationAllowed bool
	lastLog              map[string]time.Time
	nodes                corelisters.NodeLister
	pods                 corelisters.PodLister
	events               corelisters.EventLister
	nextReconcile        time.Time
}

func NewController(kube kubernetes.Interface, records clientset.Interface, dyn dynamic.Interface,
	azure AzureReader, namespace string, nodes coreinformers.NodeInformer, pods coreinformers.PodInformer,
	events coreinformers.EventInformer, clock func() time.Time) (*Controller, error) {
	if clock == nil {
		clock = time.Now
	}
	routes, err := registry(swiftMitigator{}, neverReadyMitigator{})
	if err != nil {
		return nil, err
	}
	c := &Controller{
		kube: kube, records: records, dynamic: dyn, azure: azure, namespace: namespace,
		clock: clock, observer: string(uuid.NewUUID()), routes: routes, config: Default(),
		lastLog: map[string]time.Time{},
		nodes:   nodes.Lister(), pods: pods.Lister(), events: events.Lister(),
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[clusterKey](),
			workqueue.TypedRateLimitingQueueConfig[clusterKey]{Name: ControllerName}),
	}
	for _, informer := range []cache.SharedIndexInformer{nodes.Informer(), pods.Informer(), events.Informer()} {
		c.synced = append(c.synced, informer.HasSynced)
		if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    func(any) { c.queue.Add(clusterKey{}) },
			UpdateFunc: func(any, any) { c.queue.Add(clusterKey{}) },
			DeleteFunc: func(any) { c.queue.Add(clusterKey{}) },
		}); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (c *Controller) configuration() (Config, uint64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.config, c.revision
}

func (c *Controller) SetConfig(cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	// Copy slices/selectors so a caller cannot mutate an accepted configuration.
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if len(data) > 64*1024 {
		return fmt.Errorf("mitigation configuration exceeds 64 KiB")
	}
	var copied Config
	if err := json.Unmarshal(data, &copied); err != nil {
		return err
	}
	c.mu.Lock()
	if !c.configurationAllowed && cfg.Mode != Disabled {
		c.mu.Unlock()
		return fmt.Errorf("node mitigation is not enabled for this deployment")
	}
	c.config, c.revision = copied, c.revision+1
	c.mu.Unlock()
	c.queue.Add(clusterKey{})
	return nil
}

func (c *Controller) AllowConfiguration(allowed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.configurationAllowed = allowed
	if !allowed {
		c.config = Default()
		c.revision++
	}
}

func (c *Controller) OnConfigMap(cm *corev1.ConfigMap, key string) {
	data, exists := cm.Data[key]
	if !exists {
		c.OnConfigMapDeleted()
		return
	}
	cfg, err := Parse([]byte(data))
	if err == nil {
		err = c.SetConfig(cfg)
	}
	if err != nil {
		klog.ErrorS(err, "invalid node-mitigation configuration; retaining last valid configuration")
	}
}

func (c *Controller) OnConfigMapDeleted() {
	if err := c.SetConfig(Default()); err != nil {
		klog.ErrorS(err, "disable node mitigation")
	}
}

// Every mitigation write, including state and Events, passes this boundary.
// A mode switch cannot race between authorization and submission of a write.
func (c *Controller) write(revision uint64, fn func() error) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.config.Mode != Enforce || revision != c.revision {
		return ErrPaused
	}
	return fn()
}

func (c *Controller) Run(ctx context.Context) error {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()
	ctx = utils.ContextWithControllerName(ctx, ControllerName)
	if !cache.WaitForCacheSync(ctx.Done(), c.synced...) {
		return fmt.Errorf("mitigation informer sync failed")
	}
	go func() {
		defer utilruntime.HandleCrash()
		<-ctx.Done()
		c.queue.ShutDown()
	}()
	c.queue.Add(clusterKey{})
	for {
		key, shutdown := c.queue.Get()
		if shutdown {
			return nil
		}
		if delay := c.reconcileDelay(); delay > 0 {
			c.queue.AddAfter(key, delay)
			c.queue.Done(key)
			continue
		}
		logger := utils.AddLoggerValues(utils.LoggerFromContext(ctx), key)
		reconcileCtx, cancel := context.WithTimeout(utils.ContextWithLogger(ctx, logger), 2*time.Minute)
		err := c.reconcile(reconcileCtx)
		cancel()
		c.queue.Done(key)
		if err != nil && !errors.Is(err, ErrPaused) {
			logger.Error(err, "node mitigation reconciliation failed")
			c.queue.AddRateLimited(key)
		} else {
			c.queue.Forget(key)
			cfg, _ := c.configuration()
			c.queue.AddAfter(key, cfg.retryInterval())
		}
	}
}

// Object churn cannot bypass the configured interval between cluster scans.
// Configuration changes still fence writes immediately through write().
func (c *Controller) reconcileDelay() time.Duration {
	now := c.clock()
	if delay := c.nextReconcile.Sub(now); delay > 0 {
		return delay
	}
	cfg, _ := c.configuration()
	c.nextReconcile = now.Add(cfg.retryInterval())
	return 0
}

func (c *Controller) saveEpisode(ctx context.Context, revision uint64, episode *api.MitigationEpisode) error {
	return c.write(revision, func() error {
		result, err := c.records.MgmtagentV1alpha1().MitigationEpisodes(c.namespace).UpdateStatus(ctx, episode, metav1.UpdateOptions{})
		if err == nil {
			*episode = *result
		}
		return err
	})
}

func (c *Controller) saveBudget(ctx context.Context, revision uint64, budget *api.NodeMitigationBudget) error {
	return c.write(revision, func() error {
		result, err := c.records.MgmtagentV1alpha1().NodeMitigationBudgets(c.namespace).UpdateStatus(ctx, budget, metav1.UpdateOptions{})
		if err == nil {
			*budget = *result
		}
		return err
	})
}

func (c *Controller) budget(ctx context.Context, revision uint64, mode Mode, episodesExist bool) (*api.NodeMitigationBudget, error) {
	budget, err := c.records.MgmtagentV1alpha1().NodeMitigationBudgets(c.namespace).Get(ctx, budgetName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if episodesExist {
			return nil, fmt.Errorf("mitigation accounting is missing while episodes still exist; restore the ledger")
		}
		budget = &api.NodeMitigationBudget{ObjectMeta: metav1.ObjectMeta{Name: budgetName, Namespace: c.namespace}}
		if mode == Enforce {
			err = c.write(revision, func() error {
				var createErr error
				budget, createErr = c.records.MgmtagentV1alpha1().NodeMitigationBudgets(c.namespace).Create(ctx, budget, metav1.CreateOptions{})
				return createErr
			})
		} else {
			err = nil
		}
	}
	if err != nil {
		return nil, err
	}
	if budget.Status.Pools == nil {
		budget.Status.Pools = map[string]api.PoolBaseline{}
	}
	if budget.Status.Reservations == nil {
		budget.Status.Reservations = map[string]api.MitigationReservation{}
	}
	return budget, nil
}

func episodeName(uid string) string {
	return fmt.Sprintf("node-%x", sha256.Sum256([]byte(uid)))[:45]
}

func (c *Controller) hasCandidates(cfg Config) (bool, error) {
	nodes, err := c.nodes.List(labels.Everything())
	if err != nil {
		return false, err
	}
	pods, err := c.pods.List(labels.Everything())
	if err != nil {
		return false, err
	}
	cachedEvents, err := c.events.List(labels.Everything())
	if err != nil {
		return false, err
	}
	events := make([]corev1.Event, len(cachedEvents))
	for i, event := range cachedEvents {
		events[i] = *event
	}
	for _, node := range nodes {
		if node.DeletionTimestamp != nil || node.Spec.Unschedulable {
			continue
		}
		detections, _ := nodeEvidence(node, pods, events, c.clock())
		for _, detection := range detections {
			if mitigator := c.routes[detection.Detector]; mitigator != nil && slices.Contains(cfg.Mitigators, mitigator.Name()) {
				return true, nil
			}
		}
	}
	return false, nil
}

// Cached discovery only selects work. Admission, placement and cleanup use a
// live cluster-wide snapshot so missed watch updates cannot authorize disruption.
func (c *Controller) snapshot(ctx context.Context) (ClusterSnapshot, error) {
	snapshot := ClusterSnapshot{Namespaces: map[string]*corev1.Namespace{}, NICs: map[string]map[types.UID]int64{}}
	nodes, err := c.kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return snapshot, err
	}
	for i := range nodes.Items {
		snapshot.Nodes = append(snapshot.Nodes, &nodes.Items[i])
	}
	pods, err := c.kube.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return snapshot, err
	}
	for i := range pods.Items {
		snapshot.Pods = append(snapshot.Pods, &pods.Items[i])
	}
	events, err := c.kube.CoreV1().Events("").List(ctx, metav1.ListOptions{FieldSelector: "involvedObject.kind=Pod"})
	if err != nil {
		return snapshot, err
	}
	snapshot.Events = events.Items
	snapshot.Faulted = map[string]bool{}
	for _, node := range snapshot.Nodes {
		detections, _ := nodeEvidence(node, snapshot.Pods, events.Items, c.clock())
		snapshot.Faulted[node.Name] = len(detections) > 0
	}
	namespaces, err := c.kube.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return snapshot, err
	}
	for i := range namespaces.Items {
		ns := &namespaces.Items[i]
		snapshot.Namespaces[ns.Name] = ns
	}
	nics, err := c.dynamic.Resource(mtpncGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return snapshot, fmt.Errorf("read delegated NIC allocations: %w", err)
	}
	for _, nic := range nics.Items {
		node, found, err := unstructured.NestedString(nic.Object, "status", "nodeName")
		if err != nil {
			return snapshot, err
		}
		if !found || node == "" {
			return snapshot, fmt.Errorf("delegated NIC allocation has unknown node identity")
		}
		pod, found, err := unstructured.NestedString(nic.Object, "spec", "podUID")
		if err != nil || !found || pod == "" {
			return snapshot, fmt.Errorf("delegated NIC allocation has unknown pod identity")
		}
		interfaces, _, err := unstructured.NestedSlice(nic.Object, "status", "interfaceInfos")
		if err != nil {
			return snapshot, err
		}
		count := int64(max(1, len(interfaces)))
		if snapshot.NICs[node] == nil {
			snapshot.NICs[node] = map[types.UID]int64{}
		}
		snapshot.NICs[node][types.UID(pod)] += count
	}
	return snapshot, nil
}

func nodeEvidence(node *corev1.Node, pods []*corev1.Pod, observed []corev1.Event, now time.Time) ([]detectors.Detection, []*corev1.Event) {
	events := make([]*corev1.Event, 0, len(observed))
	for i := range observed {
		if observed[i].Source.Host == node.Name {
			events = append(events, &observed[i])
		}
	}
	onNode := []*corev1.Pod{}
	for _, pod := range pods {
		if pod.Spec.NodeName == node.Name {
			onNode = append(onNode, pod)
		}
	}
	return detectors.MitigationDetections(node, events, onNode, now), events
}

func (c *Controller) reconcile(ctx context.Context) error {
	cfg, revision := c.configuration()
	episodes, err := c.records.MgmtagentV1alpha1().MitigationEpisodes(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	if cfg.Mode == Disabled && len(episodes.Items) == 0 {
		return nil
	}
	if len(episodes.Items) == 0 {
		candidates, err := c.hasCandidates(cfg)
		if err != nil {
			return err
		}
		if !candidates {
			return nil
		}
	}
	budget, err := c.budget(ctx, revision, cfg.Mode, len(episodes.Items) > 0)
	if err != nil {
		empty := &api.NodeMitigationBudget{}
		for i := range episodes.Items {
			observer := cfg
			observer.Mode = Disabled
			if observeErr := c.reconcileEpisode(ctx, observer, revision, &episodes.Items[i], empty, ClusterSnapshot{}); observeErr != nil {
				err = errors.Join(err, observeErr)
			}
		}
		return err
	}
	defer func() { reportState(episodes.Items, budget, c.clock()) }()
	snapshot, err := c.snapshot(ctx)
	if err != nil {
		utils.LoggerFromContext(ctx).Error(err, "capacity observations unavailable; observing submitted work only")
		observationConfig := cfg
		observationConfig.Mode = Disabled
		for i := range episodes.Items {
			if observeErr := c.reconcileEpisode(ctx, observationConfig, revision, &episodes.Items[i], budget, snapshot); observeErr != nil {
				err = errors.Join(err, observeErr)
			}
		}
		return err
	}
	active := map[string]bool{}
	var episodeErrors error
	for i := range episodes.Items {
		episode := &episodes.Items[i]
		active[string(episode.Spec.NodeUID)] = true
		if err := c.reconcileEpisode(ctx, cfg, revision, episode, budget, snapshot); err != nil {
			episodeErrors = errors.Join(episodeErrors, err)
		}
	}
	if episodeErrors != nil {
		return episodeErrors
	}
	if cfg.Mode == Disabled {
		return nil
	}
	for _, node := range snapshot.Nodes {
		if active[string(node.UID)] || node.DeletionTimestamp != nil {
			continue
		}
		detections, _ := nodeEvidence(node, snapshot.Pods, snapshot.Events, c.clock())
		for _, detection := range detections {
			mitigator := c.routes[detection.Detector]
			if mitigator == nil || !slices.Contains(cfg.Mitigators, mitigator.Name()) {
				continue
			}
			decision, err := mitigator.Plan(Input{Node: node, Detection: detection, Policy: cfg, Now: c.clock()})
			if err != nil {
				return err
			}
			if !actionEnabled(cfg, decision.Phase) {
				c.logCandidate(ctx, node, cfg, detection.Detector, decision.Phase, false, "action phase disabled")
				break
			}
			if node.Spec.Unschedulable {
				c.logCandidate(ctx, node, cfg, detection.Detector, decision.Phase, false, "external cordon")
				break
			}
			if len(episodes.Items) >= maxRecords || len(budget.Status.Reservations) >= maxRecords {
				c.logCandidate(ctx, node, cfg, detection.Detector, decision.Phase, false, "mitigation state limit reached")
				break
			}
			observation, instance, err := c.admission(ctx, cfg, revision, node, budget, snapshot, "")
			if err != nil {
				c.logCandidate(ctx, node, cfg, detection.Detector, decision.Phase, false, err.Error())
				break
			}
			c.logCandidate(ctx, node, cfg, detection.Detector, decision.Phase, true, "")
			if cfg.Mode == Audit {
				break
			}
			policy, err := json.Marshal(cfg)
			if err != nil {
				return err
			}
			episode := &api.MitigationEpisode{ObjectMeta: metav1.ObjectMeta{Name: episodeName(string(node.UID)), Namespace: c.namespace},
				Spec: api.MitigationEpisodeSpec{
					NodeName: node.Name, NodeUID: node.UID, ProviderID: node.Spec.ProviderID, InstanceID: instance,
					PoolID: observation.ID, Zone: node.Labels[corev1.LabelTopologyZone],
					Detector: detection.Detector, Mitigator: mitigator.Name(), Policy: string(policy), PodUIDs: detection.PodUIDs,
				}}
			err = c.write(revision, func() error {
				var e error
				episode, e = c.records.MgmtagentV1alpha1().MitigationEpisodes(c.namespace).Create(ctx, episode, metav1.CreateOptions{})
				return e
			})
			if err != nil {
				return err
			}
			episode.Status.Phase = decision.Phase
			if err = c.saveEpisode(ctx, revision, episode); err != nil {
				return err
			}
			return nil
		}
	}
	return nil
}

func (c *Controller) logCandidate(ctx context.Context, node *corev1.Node, cfg Config, detector, action string, eligible bool, reason string) {
	key := string(node.UID)
	if last, exists := c.lastLog[key]; exists && c.clock().Sub(last) < cfg.retryInterval() {
		return
	}
	if len(c.lastLog) >= maxRecords {
		clear(c.lastLog)
	}
	c.lastLog[key] = c.clock()
	utils.LoggerFromContext(ctx).Info("mitigation candidate", "node", node.Name, "nodeUID", node.UID, "detector", detector, "action", action, "mode", cfg.Mode, "candidateEligible", eligible, "reason", reason)
}

func actionEnabled(cfg Config, phase string) bool {
	switch phase {
	case PhaseCordon, PhaseRescue:
		return cfg.Rescue
	case PhaseDrain:
		return cfg.Drain
	case PhaseDelete:
		return cfg.DeleteNode
	default:
		return false
	}
}

func poolFromID(id string) string { parts := strings.Split(id, "/"); return parts[len(parts)-1] }
