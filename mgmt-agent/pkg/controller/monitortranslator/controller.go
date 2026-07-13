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

package monitortranslator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

const (
	MonitorTranslatorControllerName = "MonitorTranslator"
	fieldManager                    = "mgmt-agent-monitor-translator"
)

var (
	SourceServiceMonitorGVR = schema.GroupVersionResource{
		Group:    "monitoring.coreos.com",
		Version:  "v1",
		Resource: "servicemonitors",
	}
	SourcePodMonitorGVR = schema.GroupVersionResource{
		Group:    "monitoring.coreos.com",
		Version:  "v1",
		Resource: "podmonitors",
	}
	TargetServiceMonitorGVR = schema.GroupVersionResource{
		Group:    "azmonitoring.coreos.com",
		Version:  "v1",
		Resource: "servicemonitors",
	}
	TargetPodMonitorGVR = schema.GroupVersionResource{
		Group:    "azmonitoring.coreos.com",
		Version:  "v1",
		Resource: "podmonitors",
	}
)

// MonitorTranslatorController watches monitoring.coreos.com/v1 ServiceMonitors
// and PodMonitors and creates equivalent azmonitoring.coreos.com/v1 resources
// in namespaces matching a configurable prefix.
type MonitorTranslatorController struct {
	dynamicClient dynamic.Interface
	hasSynced     []cache.InformerSynced
	workqueue     workqueue.TypedRateLimitingInterface[string]
	nsPrefix      string

	smLister cache.GenericLister
	pmLister cache.GenericLister
}

// NewMonitorTranslatorController creates a new MonitorTranslatorController.
func NewMonitorTranslatorController(
	dynamicClient dynamic.Interface,
	serviceMonitorInformer cache.SharedIndexInformer,
	podMonitorInformer cache.SharedIndexInformer,
	nsPrefix string,
) (*MonitorTranslatorController, error) {
	c := &MonitorTranslatorController{
		dynamicClient: dynamicClient,
		hasSynced: []cache.InformerSynced{
			serviceMonitorInformer.HasSynced,
			podMonitorInformer.HasSynced,
		},
		workqueue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{Name: MonitorTranslatorControllerName},
		),
		nsPrefix: nsPrefix,
		smLister: cache.NewGenericLister(serviceMonitorInformer.GetIndexer(), schema.GroupResource{Group: SourceServiceMonitorGVR.Group, Resource: SourceServiceMonitorGVR.Resource}),
		pmLister: cache.NewGenericLister(podMonitorInformer.GetIndexer(), schema.GroupResource{Group: SourcePodMonitorGVR.Group, Resource: SourcePodMonitorGVR.Resource}),
	}

	enqueue := func(resource string) cache.ResourceEventHandlerFuncs {
		return cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj any) {
				c.enqueueObject(resource, obj)
			},
			UpdateFunc: func(_, obj any) {
				c.enqueueObject(resource, obj)
			},
			DeleteFunc: func(obj any) {
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				if ok {
					obj = tombstone.Obj
				}
				c.enqueueObject(resource, obj)
			},
		}
	}

	if _, err := serviceMonitorInformer.AddEventHandler(enqueue(SourceServiceMonitorGVR.Resource)); err != nil {
		return nil, fmt.Errorf("failed to add ServiceMonitor event handler: %w", err)
	}
	if _, err := podMonitorInformer.AddEventHandler(enqueue(SourcePodMonitorGVR.Resource)); err != nil {
		return nil, fmt.Errorf("failed to add PodMonitor event handler: %w", err)
	}

	return c, nil
}

func (c *MonitorTranslatorController) enqueueObject(resource string, obj any) {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		return
	}
	c.workqueue.Add(resource + "/" + key)
}

// Run starts the controller workers and blocks until the context is cancelled.
func (c *MonitorTranslatorController) Run(ctx context.Context, workers int) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	logger := klog.FromContext(ctx)
	logger.Info("Starting MonitorTranslator controller")

	logger.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(ctx.Done(), c.hasSynced...); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	logger.Info("Starting workers", "count", workers)
	for range workers {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	logger.Info("MonitorTranslator controller started")
	<-ctx.Done()
	logger.Info("Shutting down MonitorTranslator controller")

	return nil
}

func (c *MonitorTranslatorController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *MonitorTranslatorController) processNextWorkItem(ctx context.Context) bool {
	key, shutdown := c.workqueue.Get()
	if shutdown {
		return false
	}
	defer c.workqueue.Done(key)

	err := c.syncHandler(ctx, key)
	if err == nil {
		c.workqueue.Forget(key)
		return true
	}

	utilruntime.HandleError(fmt.Errorf("error syncing %q: %w", key, err))
	c.workqueue.AddRateLimited(key)
	return true
}

// syncHandler processes a single work item. The key format is "<resource>/<namespace>/<name>".
func (c *MonitorTranslatorController) syncHandler(ctx context.Context, key string) error {
	resource, nsName, ok := strings.Cut(key, "/")
	if !ok {
		return fmt.Errorf("invalid key %q: missing resource prefix", key)
	}
	namespace, name, err := cache.SplitMetaNamespaceKey(nsName)
	if err != nil {
		return fmt.Errorf("invalid key %q: %w", key, err)
	}

	if !strings.HasPrefix(namespace, c.nsPrefix) {
		return nil
	}

	logger := klog.FromContext(ctx).WithValues("resource", resource, "namespace", namespace, "name", name)
	ctx = klog.NewContext(ctx, logger)

	var sourceGVR, targetGVR schema.GroupVersionResource
	var lister cache.GenericLister
	switch resource {
	case SourceServiceMonitorGVR.Resource:
		sourceGVR = SourceServiceMonitorGVR
		targetGVR = TargetServiceMonitorGVR
		lister = c.smLister
	case SourcePodMonitorGVR.Resource:
		sourceGVR = SourcePodMonitorGVR
		targetGVR = TargetPodMonitorGVR
		lister = c.pmLister
	default:
		return fmt.Errorf("unknown resource %q in key %q", resource, key)
	}

	obj, err := lister.ByNamespace(namespace).Get(name)
	if err != nil {
		logger.V(4).Info("Source resource no longer exists, translated resource will be garbage collected via OwnerReference")
		return nil
	}

	source, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("expected *unstructured.Unstructured, got %T", obj)
	}

	translated := Translate(source, sourceGVR, targetGVR)
	if err := c.applyResource(ctx, targetGVR, translated); err != nil {
		return fmt.Errorf("failed to apply translated %s %s/%s: %w", resource, namespace, name, err)
	}

	logger.V(4).Info("Translated monitor resource")
	return nil
}

func (c *MonitorTranslatorController) applyResource(ctx context.Context, gvr schema.GroupVersionResource, desired *unstructured.Unstructured) error {
	data, err := json.Marshal(desired)
	if err != nil {
		return fmt.Errorf("failed to marshal resource: %w", err)
	}
	_, err = c.dynamicClient.Resource(gvr).Namespace(desired.GetNamespace()).Patch(
		ctx, desired.GetName(), types.ApplyPatchType, data,
		metav1.PatchOptions{FieldManager: fieldManager, Force: ptr.To(true)},
	)
	return err
}

// Translate creates an azmonitoring.coreos.com/v1 resource from a monitoring.coreos.com/v1 source.
// The spec is copied verbatim. An OwnerReference is set for garbage collection.
func Translate(source *unstructured.Unstructured, sourceGVR, targetGVR schema.GroupVersionResource) *unstructured.Unstructured {
	target := &unstructured.Unstructured{Object: make(map[string]any)}
	target.SetAPIVersion(targetGVR.Group + "/v1")
	target.SetKind(source.GetKind())
	target.SetName(source.GetName())
	target.SetNamespace(source.GetNamespace())

	if labels := source.GetLabels(); len(labels) > 0 {
		target.SetLabels(labels)
	}

	spec, found, _ := unstructured.NestedMap(source.Object, "spec")
	if found {
		_ = unstructured.SetNestedMap(target.Object, spec, "spec")
	}

	target.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion: source.GetAPIVersion(),
			Kind:       source.GetKind(),
			Name:       source.GetName(),
			UID:        source.GetUID(),
		},
	})

	return target
}
