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

package swiftpod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
)

var ErrPaused = errors.New("SWIFT mitigation paused by configuration change")

type podKey struct{ namespace, name string }

// AddLoggerValues attaches the Pod's namespace and name to log entries.
func (key podKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues("namespace", key.namespace, "pod", key.name)
}

type Controller struct {
	kube                 kubernetes.Interface
	dynamic              dynamic.Interface
	namespace            string
	clock                func() time.Time
	queue                workqueue.TypedRateLimitingInterface[podKey]
	pods                 corelisters.PodLister
	events               corelisters.EventLister
	nodes                corelisters.NodeLister
	synced               []cache.InformerSynced
	mu                   sync.RWMutex
	config               Config
	revision             uint64
	configurationAllowed bool
}

// NewController creates a disabled controller and registers its informer handlers.
func NewController(kube kubernetes.Interface, dyn dynamic.Interface, namespace string,
	nodes coreinformers.NodeInformer, pods coreinformers.PodInformer, events coreinformers.EventInformer,
	clock func() time.Time) (*Controller, error) {
	if clock == nil {
		clock = time.Now
	}
	c := &Controller{kube: kube, dynamic: dyn, namespace: namespace, clock: clock, config: Default(),
		pods: pods.Lister(), events: events.Lister(), nodes: nodes.Lister(),
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(workqueue.DefaultTypedControllerRateLimiter[podKey](),
			workqueue.TypedRateLimitingQueueConfig[podKey]{Name: ControllerName})}
	for _, informer := range []cache.SharedIndexInformer{nodes.Informer(), pods.Informer(), events.Informer()} {
		c.synced = append(c.synced, informer.HasSynced)
		if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: c.enqueue, UpdateFunc: func(_, obj any) { c.enqueue(obj) }, DeleteFunc: c.enqueue,
		}); err != nil {
			c.queue.ShutDown()
			return nil, err
		}
	}
	return c, nil
}

// Queues router Pods affected by Pod, sandbox Event or Node changes.
func (c *Controller) enqueue(obj any) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	switch obj := obj.(type) {
	case *corev1.Pod:
		if obj.Labels["app"] == "private-router" {
			c.queue.Add(podKey{obj.Namespace, obj.Name})
		}
	case *corev1.Event:
		if obj.InvolvedObject.Kind == "Pod" && obj.Reason == "FailedCreatePodSandBox" {
			c.queue.Add(podKey{obj.InvolvedObject.Namespace, obj.InvolvedObject.Name})
		}
	case *corev1.Node:
		c.enqueueAll()
	}
}

// Queues every cached private-router Pod for reevaluation.
func (c *Controller) enqueueAll() {
	pods, err := c.pods.List(labels.SelectorFromSet(labels.Set{"app": "private-router"}))
	if err != nil {
		klog.ErrorS(err, "list SWIFT router candidates")
		return
	}
	for _, pod := range pods {
		c.enqueue(pod)
	}
}

// Reads the accepted configuration and its revision under the configuration lock.
func (c *Controller) configuration() (Config, uint64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.config, c.revision
}

// SetConfig validates and copies configuration, advances its revision and requeues router Pods.
func (c *Controller) SetConfig(cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	copied, err := Parse(data)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if !c.configurationAllowed && cfg.Mode != Disabled {
		c.mu.Unlock()
		return fmt.Errorf("SWIFT mitigation is not enabled for this deployment")
	}
	c.config, c.revision = copied, c.revision+1
	c.mu.Unlock()
	c.enqueueAll()
	return nil
}

// AllowConfiguration gates audit and enforce modes, resetting to disabled when disallowed.
func (c *Controller) AllowConfiguration(allowed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.configurationAllowed = allowed
	if !allowed {
		c.config = Default()
		c.revision++
	}
}

// OnConfigMap applies valid configuration, disables on a missing key and retains policy on errors.
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
		klog.ErrorS(err, "invalid SWIFT configuration; retaining last valid configuration")
	}
}

// OnConfigMapDeleted disables mitigation when its configuration disappears.
func (c *Controller) OnConfigMapDeleted() {
	if err := c.SetConfig(Default()); err != nil {
		klog.ErrorS(err, "disable SWIFT mitigation")
	}
}

// Runs a write only in enforce mode at the expected revision, blocking config changes until it returns.
func (c *Controller) write(revision uint64, fn func() error) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.config.Mode != Enforce || revision != c.revision {
		return ErrPaused
	}
	return fn()
}

// Run waits for informer caches and processes queued Pods with configuration-aware retries.
func (c *Controller) Run(ctx context.Context) error {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()
	ctx = utils.ContextWithControllerName(ctx, ControllerName)
	ctx = utils.ContextWithLogger(ctx, utils.LoggerFromContext(ctx).WithValues(
		utils.LogValues{}.AddControllerName(ControllerName)...))
	if !cache.WaitForCacheSync(ctx.Done(), c.synced...) {
		return fmt.Errorf("SWIFT informer sync failed")
	}
	go func() {
		defer utilruntime.HandleCrash()
		<-ctx.Done()
		c.queue.ShutDown()
	}()
	c.enqueueAll()
	next := map[podKey]time.Time{}
	var scheduledRevision uint64
	for {
		key, shutdown := c.queue.Get()
		if shutdown {
			return nil
		}
		cfg, revision := c.configuration()
		if revision != scheduledRevision {
			next, scheduledRevision = map[podKey]time.Time{}, revision
		}
		logger := utils.AddLoggerValues(utils.LoggerFromContext(ctx), key)
		if cfg.Mode == Disabled {
			delete(next, key)
		} else if delay := next[key].Sub(c.clock()); delay > 0 {
			c.queue.AddAfter(key, delay)
		} else {
			retry, err := c.reconcile(utils.ContextWithLogger(ctx, logger), key, cfg, revision)
			if err != nil {
				logger.Error(err, "SWIFT router mitigation held")
			}
			if retry {
				next[key] = c.clock().Add(cfg.retryInterval())
				c.queue.AddAfter(key, cfg.retryInterval())
			} else {
				delete(next, key)
			}
		}
		c.queue.Forget(key)
		c.queue.Done(key)
	}
}

// Screens cached candidates and evaluates rescue against live observations.
func (c *Controller) reconcile(ctx context.Context, key podKey, cfg Config, revision uint64) (bool, error) {
	if cfg.Mode == Disabled {
		return false, nil
	}
	pod, err := c.pods.Pods(key.namespace).Get(key.name)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if podReady(pod) || terminal(pod) || pod.DeletionTimestamp != nil ||
		pod.Labels["app"] != "private-router" || !selected(cfg.Workload.PodSelector, pod.Labels) || pod.Spec.NodeName == "" {
		return false, nil
	}
	node, err := c.nodes.Get(pod.Spec.NodeName)
	if err != nil {
		return true, err
	}
	events, err := c.events.Events(pod.Namespace).List(labels.Everything())
	if err != nil {
		return true, err
	}
	if !swiftNode(node) || !stalled(pod, events, c.clock()) {
		return true, nil
	}
	snapshot, err := c.snapshot(ctx)
	if err != nil {
		return true, err
	}
	return true, c.rescue(ctx, cfg, revision, node, pod, snapshot)
}
