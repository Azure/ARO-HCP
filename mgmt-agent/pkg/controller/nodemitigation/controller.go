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
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/detection"
	clientset "github.com/Azure/ARO-HCP/mgmt-agent/pkg/generated/clientset/versioned"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/mitigation"
)

var (
	ErrPaused = errors.New("mitigation writes paused by mode or configuration change")
)

const (
	budgetName = "node-mitigation"
	maxRecords = 1024
)

type clusterKey struct{}

func (clusterKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(utils.LogValues{}.AddControllerName(ControllerName)...)
}

type Controller struct {
	kube                 kubernetes.Interface
	records              clientset.Interface
	dynamic              dynamic.Interface
	namespace            string
	clock                func() time.Time
	mitigators           *mitigation.Registry
	detectors            *detection.Registry
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
	namespace string, nodes coreinformers.NodeInformer, pods coreinformers.PodInformer,
	events coreinformers.EventInformer, clock func() time.Time, detectorRegistry *detection.Registry, mitigatorRegistry *mitigation.Registry) (*Controller, error) {
	if clock == nil {
		clock = time.Now
	}
	if detectorRegistry == nil || mitigatorRegistry == nil {
		return nil, fmt.Errorf("detector and mitigator registries are required")
	}
	c := &Controller{
		kube: kube, records: records, dynamic: dyn, namespace: namespace,
		clock: clock, detectors: detectorRegistry, mitigators: mitigatorRegistry, config: Default(),
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
	if err := cfg.Validate(c.mitigators); err != nil {
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
	cfg, err := Parse([]byte(data), c.mitigators)
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
		detections := c.nodeEvidence(node, pods, events, c.clock())
		for _, detection := range detections {
			if mitigator := c.mitigators.ForDetector(detection.Detector); mitigator != nil && slices.Contains(cfg.Mitigators, mitigator.Name()) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (c *Controller) reconcile(ctx context.Context) error {
	cfg, revision := c.configuration()
	budget, err := c.budget(ctx, revision, cfg.Mode)
	if err != nil {
		return err
	}
	candidates := false
	if cfg.Mode != Disabled {
		candidates, err = c.hasCandidates(cfg)
	}
	if err != nil {
		return err
	}
	if !candidates {
		return c.pruneBudget(ctx, cfg, revision, budget)
	}
	snapshot, err := c.snapshot(ctx)
	if err != nil {
		return err
	}
	if err := c.pruneBudget(ctx, cfg, revision, budget); err != nil {
		return err
	}
	var operationErrors error
	for _, node := range snapshot.Nodes {
		if node.DeletionTimestamp != nil || node.Spec.Unschedulable {
			continue
		}
		detections := c.nodeEvidence(node, snapshot.Pods, snapshot.Events, c.clock())
		for _, detection := range detections {
			mitigator := c.mitigators.ForDetector(detection.Detector)
			if mitigator == nil || !slices.Contains(cfg.Mitigators, mitigator.Name()) {
				continue
			}
			decision, err := mitigator.Plan(mitigation.Input{Detection: detection})
			if err != nil {
				return errors.Join(err, operationErrors)
			}
			if decision.Hold != "" {
				c.logCandidate(ctx, node, cfg, detection.Detector, decision.Action, false, decision.Hold)
				continue
			}
			var acted bool
			switch decision.Action {
			case mitigation.ActionEvict:
				evictor, ok := mitigator.(mitigation.PodEvictor)
				if !ok {
					err = fmt.Errorf("mitigator %q does not implement Pod eviction policy", mitigator.Name())
					break
				}
				acted, err = c.rescue(ctx, cfg, revision, node, detection, budget, snapshot, evictor)
			default:
				err = fmt.Errorf("unsupported mitigation action %q", decision.Action)
			}
			if err != nil {
				c.logCandidate(ctx, node, cfg, detection.Detector, decision.Action, false, err.Error())
				operationErrors = errors.Join(operationErrors, err)
			}
			if acted {
				return operationErrors
			}
		}
	}
	return operationErrors
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
