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

	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	MonitorTranslatorControllerName = "monitor-translator"
	fieldManager                    = "mgmt-agent-monitor-translator"

	// ksmNameLabelKey/ksmNameLabelValue identify the per-HCP kube-state-metrics
	// monitor that the ksmhcp controller creates directly in the configured
	// monitoring API group (must stay in sync with ksmhcp's resource label). The
	// translator must never translate it: in AMA mode ksmhcp already emits the
	// azmonitoring resource, so translating the source would collide on the same
	// name/namespace object. ExcludeKSMSelector filters it out at the informer.
	ksmNameLabelKey    = "app.kubernetes.io/name"
	ksmNameLabelValue  = "kube-state-metrics-hcp"
	ExcludeKSMSelector = ksmNameLabelKey + "!=" + ksmNameLabelValue
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
// and PodMonitors that are owned by a HostedControlPlane and creates equivalent
// azmonitoring.coreos.com/v1 resources so AMA can discover them. Monitors that
// are not owned by a HostedControlPlane (see hasHCPOwner) are ignored, as is the
// kube-state-metrics monitor the ksmhcp controller emits directly in the target
// group (see isKSMManaged / ExcludeKSMSelector).
//
// Each translated resource carries an OwnerReference to its source monitor, so
// deleting the source garbage-collects the translation. Known limitation: on an
// AMA -> OSS rollback the controller stops running and no longer prunes the
// azmonitoring.coreos.com resources it created; they are orphaned until the
// owning HostedControlPlane (a transitive owner via the source monitor) is
// deleted, which garbage-collects them. Cleanup relies on HCP GC only.
type MonitorTranslatorController struct {
	dynamicClient dynamic.Interface
	hasSynced     []cache.InformerSynced
	workqueue     workqueue.TypedRateLimitingInterface[string]

	smLister cache.GenericLister
	pmLister cache.GenericLister

	// metricsRegion/metricsEnvironment are stamped onto translated series as
	// set-if-absent relabelings, preserving parity with OSS (which supplies them
	// via the agent's externalLabels). Required in AMA mode; see cmd/options.go.
	metricsRegion      string
	metricsEnvironment string
}

// NewMonitorTranslatorController creates a new MonitorTranslatorController.
func NewMonitorTranslatorController(
	dynamicClient dynamic.Interface,
	serviceMonitorInformer cache.SharedIndexInformer,
	podMonitorInformer cache.SharedIndexInformer,
	metricsRegion string,
	metricsEnvironment string,
) (*MonitorTranslatorController, error) {
	c := &MonitorTranslatorController{
		dynamicClient:      dynamicClient,
		metricsRegion:      metricsRegion,
		metricsEnvironment: metricsEnvironment,
		hasSynced: []cache.InformerSynced{
			serviceMonitorInformer.HasSynced,
			podMonitorInformer.HasSynced,
		},
		workqueue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{Name: MonitorTranslatorControllerName},
		),
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

	ctx = utils.ContextWithControllerName(ctx, MonitorTranslatorControllerName)
	logger := klog.FromContext(ctx).WithValues(utils.LogValues{}.AddControllerName(MonitorTranslatorControllerName)...)
	ctx = klog.NewContext(ctx, logger)
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

	ctx = utils.ContextWithLogger(ctx, utils.AddLoggerValues(utils.LoggerFromContext(ctx), key))

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
func hasHCPOwner(obj *unstructured.Unstructured) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.APIVersion == "hypershift.openshift.io/v1beta1" && ref.Kind == "HostedControlPlane" {
			return true
		}
	}
	return false
}

// isKSMManaged reports whether the monitor is the kube-state-metrics monitor the
// ksmhcp controller manages directly, which the translator must leave alone.
func isKSMManaged(obj *unstructured.Unstructured) bool {
	return obj.GetLabels()[ksmNameLabelKey] == ksmNameLabelValue
}

func (c *MonitorTranslatorController) syncHandler(ctx context.Context, key string) error {
	resource, nsName, found := strings.Cut(key, "/")
	if !found {
		return fmt.Errorf("invalid key %q: missing resource prefix", key)
	}
	namespace, name, err := cache.SplitMetaNamespaceKey(nsName)
	if err != nil {
		return fmt.Errorf("invalid key %q: %w", key, err)
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

	if !hasHCPOwner(source) {
		return nil
	}

	if isKSMManaged(source) {
		// Backstop for the informer-level ExcludeKSMSelector: the ksmhcp
		// controller already emits this monitor in the target group directly, so
		// translating it would collide on the same object.
		logger.V(4).Info("Skipping KSM-managed monitor; created directly by the ksmhcp controller")
		return nil
	}

	translated := Translate(source, sourceGVR, targetGVR, c.metricsRegion, c.metricsEnvironment)
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

// accountTargetLabel is the per-series routing label that tells AMA which Azure
// Monitor account (workspace) a series belongs to. HCP series are stamped with
// value "hcp" so the HCP DCR's labelIncludeFilter (keyed on this same label)
// ingests them. This must be microsoft_metrics_account, the per-series label;
// microsoft_metrics_include_label is only the DCR filter key, not a series label.
const accountTargetLabel = "microsoft_metrics_account"

const (
	regionTargetLabel      = "region"
	environmentTargetLabel = "environment"
)

// injectLabels appends the AMA routing and parity relabelings to every endpoint
// under spec[key]. When absent, it adds:
//   - the microsoft_metrics_account=hcp routing label, so the series reaches the
//     HCP workspace via the HCP DCR's labelIncludeFilter;
//   - set-if-absent region/environment labels, to preserve parity with OSS. In
//     OSS mode the agent supplies these via externalLabels; AMA has no agent, so
//     the translator stamps them here.
//
// Each relabel is only appended if an entry with the same targetLabel is not
// already present, so re-translation is idempotent. Empty region/environment
// values are skipped (they are required and validated in AMA mode, so this is
// only defensive).
func injectLabels(spec map[string]any, key, metricsRegion, metricsEnvironment string) {
	endpoints, ok := spec[key].([]any)
	if !ok {
		return
	}
	for _, endpoint := range endpoints {
		endpointMap, ok := endpoint.(map[string]any)
		if !ok {
			continue
		}
		relabelConfigs, _ := endpointMap["metricRelabelings"].([]any)
		relabelConfigs = appendRelabelIfAbsent(relabelConfigs, accountTargetLabel, map[string]any{
			"targetLabel": accountTargetLabel,
			"replacement": "hcp",
			"action":      "replace",
		})
		if metricsRegion != "" {
			relabelConfigs = appendRelabelIfAbsent(relabelConfigs, regionTargetLabel, setIfAbsentRelabel(regionTargetLabel, metricsRegion))
		}
		if metricsEnvironment != "" {
			relabelConfigs = appendRelabelIfAbsent(relabelConfigs, environmentTargetLabel, setIfAbsentRelabel(environmentTargetLabel, metricsEnvironment))
		}
		endpointMap["metricRelabelings"] = relabelConfigs
	}
}

// setIfAbsentRelabel returns a relabeling that sets label to value only when the
// series does not already carry it. The regex "^$" matches an empty label value,
// so a series that already has the label is left untouched.
func setIfAbsentRelabel(label, value string) map[string]any {
	return map[string]any{
		"sourceLabels": []any{label},
		"regex":        "^$",
		"targetLabel":  label,
		"replacement":  value,
		"action":       "replace",
	}
}

// appendRelabelIfAbsent appends relabel to configs unless an entry already
// targets targetLabel (e.g. the KSM monitor sets the routing label at creation
// time), keeping translation idempotent.
func appendRelabelIfAbsent(configs []any, targetLabel string, relabel map[string]any) []any {
	if hasTargetLabel(configs, targetLabel) {
		return configs
	}
	return append(configs, relabel)
}

func hasTargetLabel(relabelConfigs []any, targetLabel string) bool {
	for _, rc := range relabelConfigs {
		rcMap, ok := rc.(map[string]any)
		if !ok {
			continue
		}
		if target, _ := rcMap["targetLabel"].(string); target == targetLabel {
			return true
		}
	}
	return false
}

// Translate creates an azmonitoring.coreos.com/v1 resource from a monitoring.coreos.com/v1 source.
// The spec is copied verbatim, then the AMA routing and region/environment parity
// relabelings are injected (see injectLabels). An OwnerReference is set for
// garbage collection.
func Translate(source *unstructured.Unstructured, sourceGVR, targetGVR schema.GroupVersionResource, metricsRegion, metricsEnvironment string) *unstructured.Unstructured {
	target := &unstructured.Unstructured{Object: make(map[string]any)}
	target.SetAPIVersion(targetGVR.Group + "/v1")
	target.SetKind(source.GetKind())
	target.SetName(source.GetName())
	target.SetNamespace(source.GetNamespace())

	if labels := source.GetLabels(); len(labels) > 0 {
		target.SetLabels(labels)
	}

	spec, found, err := unstructured.NestedMap(source.Object, "spec")
	if err != nil {
		klog.Warningf("failed to read spec from %s/%s %s/%s: %v", sourceGVR.Group, sourceGVR.Resource, source.GetNamespace(), source.GetName(), err)
	}
	if found {
		injectLabels(spec, "endpoints", metricsRegion, metricsEnvironment)
		injectLabels(spec, "podMetricsEndpoints", metricsRegion, metricsEnvironment)
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
