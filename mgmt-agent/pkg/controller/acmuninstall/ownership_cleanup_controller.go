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

package acmuninstall

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	acmOwnershipCleanupControllerName = "acm-ownership-cleanup"

	ownershipCleanupWorkers         = 4
	ownershipCleanupRecheckInterval = 30 * time.Second

	manifestWorkDeletePropagationOrphan = "Orphan"

	stepEnsureManifestWorksOrphan                  = "ensure-manifestworks-orphan"
	stepClearDeletingAppliedManifestWorkFinalizers = "clear-deleting-appliedmanifestwork-finalizers"
	stepStripDanglingAppliedManifestWorkOwnerRefs  = "strip-dangling-appliedmanifestwork-ownerrefs"
)

var ManifestWorksGVR = schema.GroupVersionResource{
	Group: "work.open-cluster-management.io", Version: "v1", Resource: "manifestworks",
}

// ownershipCleanupKey identifies one HostedCluster-related resource to clean up.
type ownershipCleanupKey struct {
	gvr       schema.GroupVersionResource
	namespace string
	name      string
}

func (key ownershipCleanupKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		"gvr", key.gvr.String(),
		"namespace", key.namespace,
		"name", key.name,
	)
}

func ownershipCleanupKeyFor(ownedResource ownershipResource) ownershipCleanupKey {
	return ownershipCleanupKey{
		gvr:       ownedResource.gvr,
		namespace: ownedResource.obj.GetNamespace(),
		name:      ownedResource.obj.GetName(),
	}
}

// resourceCleanupStep runs one ordered action against a single owned resource.
type resourceCleanupStep struct {
	name string
	run  func(context.Context, *ownershipResource) (stepResult, error)
}

// OwnershipCleanupController releases HostedCluster-related resources from ACM
// AppliedManifestWork ownership so the ACM uninstall prerequisites can pass.
//
// Temporary: remove this controller once ACM/MCE has been uninstalled from all
// production management clusters (same cleanup as the ACM uninstall controller).
//
// Producer runs once: lists HostedCluster ownership resources (same set as
// ensureHostedClusterResourcesNotACMOwned) plus PodNetworks and
// PodNetworkInstances, and hands each eligible candidate to the consumer queue,
// then stops.
//
// Consumers (ownershipCleanupWorkers) process queue items. Per resource,
// ordered steps each perform at most one write; if a step is not done the
// resource is requeued:
//  1. Set ManifestWork spec.deleteOption.propagationPolicy=Orphan when the
//     ManifestWork still exists and is not deleting
//  2. Clear finalizers on AppliedManifestWorks that are deleting
//  3. Strip AppliedManifestWork ownerReferences (last, so it does not block
//     earlier steps)
type OwnershipCleanupController struct {
	name          string
	dynamicClient dynamic.Interface
	queue         workqueue.TypedRateLimitingInterface[ownershipCleanupKey]
	steps         []resourceCleanupStep
}

func NewOwnershipCleanupController(dynamicClient dynamic.Interface) (*OwnershipCleanupController, error) {
	if dynamicClient == nil {
		return nil, fmt.Errorf("dynamic client is required")
	}
	controller := &OwnershipCleanupController{
		name:          acmOwnershipCleanupControllerName,
		dynamicClient: dynamicClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[ownershipCleanupKey](),
			workqueue.TypedRateLimitingQueueConfig[ownershipCleanupKey]{Name: acmOwnershipCleanupControllerName},
		),
	}
	controller.steps = []resourceCleanupStep{
		{name: stepEnsureManifestWorksOrphan, run: controller.ensureManifestWorksOrphan},
		{name: stepClearDeletingAppliedManifestWorkFinalizers, run: controller.ensureDeletingAppliedManifestWorkFinalizersCleared},
		{name: stepStripDanglingAppliedManifestWorkOwnerRefs, run: controller.ensureDanglingAppliedManifestWorkOwnerRefsRemoved},
	}
	return controller, nil
}

func (c *OwnershipCleanupController) Run(ctx context.Context) error {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	ctx = utils.ContextWithControllerName(ctx, c.name)
	logger := utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting ACM ownership cleanup controller")

	logger.Info("Starting workers", "count", ownershipCleanupWorkers)
	for range ownershipCleanupWorkers {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	logger.Info("Starting ACM ownership cleanup producer")
	for {
		err := c.produce(ctx)
		if err == nil { // only needs to be done once and the workers requeues if the work isn't done
			break
		}
		logger.Error(err, "ACM ownership cleanup producer failed; retrying")
		select {
		case <-ctx.Done():
			logger.Info("Stopped ACM ownership cleanup controller")
			return nil
		case <-time.After(ownershipCleanupRecheckInterval):
		}
	}
	logger.Info("ACM ownership cleanup producer finished")

	<-ctx.Done()
	logger.Info("Stopped ACM ownership cleanup controller")
	return nil
}

func (c *OwnershipCleanupController) runWorker(ctx context.Context) {
	for c.processNext(ctx) {
	}
}

func (c *OwnershipCleanupController) processNext(ctx context.Context) bool {
	key, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(key)

	logger := utils.AddLoggerValues(utils.LoggerFromContext(ctx), key)
	ctx = utils.ContextWithLogger(ctx, logger)

	if err := c.syncResource(ctx, key); err != nil {
		logger.Error(err, "ACM ownership cleanup sync failed; retrying")
		c.queue.AddRateLimited(key)
		return true
	}
	c.queue.Forget(key)
	return true
}

// produce lists eligible ownership resources once and enqueues each candidate
// for the consumers. It does not use the workqueue for its own scheduling;
// after a successful pass it stops.
func (c *OwnershipCleanupController) produce(ctx context.Context) error {
	logger := utils.LoggerFromContext(ctx)

	resources, err := listOwnershipCleanupCandidates(ctx, c.dynamicClient)
	if err != nil {
		return err
	}

	seenKeys := map[ownershipCleanupKey]struct{}{}
	for i := range resources {
		ownedResource := resources[i]
		if len(appliedManifestWorkOwnerReferences(&ownedResource.obj)) == 0 {
			continue
		}
		resourceKey := ownershipCleanupKeyFor(ownedResource)
		if _, alreadyQueued := seenKeys[resourceKey]; alreadyQueued {
			continue
		}
		seenKeys[resourceKey] = struct{}{}
		c.queue.Add(resourceKey)
		logger.Info("ACM ownership cleanup producer enqueued candidate",
			"resource", objectRef(&ownedResource.obj),
			"gvr", resourceKey.gvr.String(),
		)
	}
	return nil
}

func (c *OwnershipCleanupController) syncResource(ctx context.Context, key ownershipCleanupKey) error {
	logger := utils.LoggerFromContext(ctx)

	ownedResource, err := c.getOwnershipResource(ctx, key)
	if err != nil {
		return err
	}
	if ownedResource == nil {
		logger.Info("owned resource gone; nothing to clean")
		return nil
	}

	for _, cleanupStep := range c.steps {
		stepLogger := logger.WithValues("step", cleanupStep.name)
		result, err := cleanupStep.run(ctx, ownedResource)
		if err != nil {
			return fmt.Errorf("step %q: %w", cleanupStep.name, err)
		}
		if result.done {
			stepLogger.Info("ACM ownership cleanup step completed")
			continue
		}
		stepLogger.WithValues("reason", result.reason).Info("ACM ownership cleanup step not done", result.values...)
		c.queue.AddAfter(key, ownershipCleanupRecheckInterval)
		return nil
	}

	logger.Info("ACM ownership cleanup finished steps for resource")
	return nil
}

func (c *OwnershipCleanupController) getOwnershipResource(ctx context.Context, key ownershipCleanupKey) (*ownershipResource, error) {
	var (
		object *unstructured.Unstructured
		err    error
	)
	if len(key.namespace) == 0 {
		object, err = c.dynamicClient.Resource(key.gvr).Get(ctx, key.name, metav1.GetOptions{})
	} else {
		object, err = c.dynamicClient.Resource(key.gvr).Namespace(key.namespace).Get(ctx, key.name, metav1.GetOptions{})
	}
	if apiGone(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get %s %s/%s: %w", key.gvr.String(), key.namespace, key.name, err)
	}
	return &ownershipResource{gvr: key.gvr, obj: *object}, nil
}

// ensureManifestWorksOrphan sets deleteOption.propagationPolicy=Orphan on
// ManifestWorks referenced by this resource that still exist and are not
// deleting. One ManifestWork is updated per sync; the next pass verifies.
func (c *OwnershipCleanupController) ensureManifestWorksOrphan(ctx context.Context, ownedResource *ownershipResource) (stepResult, error) {
	logger := utils.LoggerFromContext(ctx)
	seenManifestWorks := map[string]struct{}{}
	for _, ownerReference := range appliedManifestWorkOwnerReferences(&ownedResource.obj) {
		manifestWork, err := c.findManifestWork(ctx, ownerReference.Name, "")
		if err != nil {
			return stepResult{}, err
		}
		// If the ManifestWork is already deleting, skip it: clearing finalizers
		// on the AppliedManifestWork is enough to unstick deletion.
		if manifestWork == nil || !manifestWork.GetDeletionTimestamp().IsZero() {
			continue
		}
		manifestWorkKey := manifestWork.GetNamespace() + "/" + manifestWork.GetName()
		if _, alreadySeen := seenManifestWorks[manifestWorkKey]; alreadySeen {
			continue
		}
		seenManifestWorks[manifestWorkKey] = struct{}{}

		if manifestWorkPropagationPolicy(manifestWork) == manifestWorkDeletePropagationOrphan {
			continue
		}

		logger.Info("setting ManifestWork deleteOption propagationPolicy to Orphan", "manifestwork", manifestWorkKey)
		applyObject := &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "work.open-cluster-management.io/v1",
			"kind":       "ManifestWork",
			"metadata": map[string]interface{}{
				"name":      manifestWork.GetName(),
				"namespace": manifestWork.GetNamespace(),
			},
			"spec": map[string]interface{}{
				"deleteOption": map[string]interface{}{
					"propagationPolicy": manifestWorkDeletePropagationOrphan,
				},
			},
		}}
		applyData, err := json.Marshal(applyObject.Object)
		if err != nil {
			return stepResult{}, fmt.Errorf("marshal ManifestWork %s apply patch: %w", manifestWorkKey, err)
		}
		_, err = c.dynamicClient.Resource(ManifestWorksGVR).Namespace(manifestWork.GetNamespace()).Patch(
			ctx,
			manifestWork.GetName(),
			types.ApplyPatchType,
			applyData,
			metav1.PatchOptions{
				FieldManager: acmOwnershipCleanupControllerName,
				Force:        ptr.To(true),
			},
		)
		if apiGone(err) {
			continue
		}
		if err != nil {
			return stepResult{}, fmt.Errorf("apply ManifestWork %s deleteOption: %w", manifestWorkKey, err)
		}
		return stepResult{
			done:   false,
			reason: "updated ManifestWork deleteOption to Orphan; verifying on next reconcile",
			values: []any{"manifestwork", manifestWorkKey},
		}, nil
	}

	return stepResult{done: true}, nil
}

// ensureDeletingAppliedManifestWorkFinalizersCleared clears finalizers on
// AppliedManifestWorks referenced by this resource that are deleting. One
// AppliedManifestWork is updated per sync; the next pass verifies.
func (c *OwnershipCleanupController) ensureDeletingAppliedManifestWorkFinalizersCleared(ctx context.Context, ownedResource *ownershipResource) (stepResult, error) {
	logger := utils.LoggerFromContext(ctx)
	for _, ownerReference := range appliedManifestWorkOwnerReferences(&ownedResource.obj) {
		appliedManifestWork, err := c.getAppliedManifestWork(ctx, ownerReference.Name)
		if err != nil {
			return stepResult{}, err
		}
		if appliedManifestWork == nil || appliedManifestWork.GetDeletionTimestamp().IsZero() || len(appliedManifestWork.GetFinalizers()) == 0 {
			continue
		}

		logger.Info("removing AppliedManifestWork finalizers",
			"appliedmanifestwork", ownerReference.Name,
			"finalizers", appliedManifestWork.GetFinalizers(),
		)
		updatedAppliedManifestWork := appliedManifestWork.DeepCopy()
		updatedAppliedManifestWork.SetFinalizers(nil)
		_, err = c.dynamicClient.Resource(AppliedManifestWorksGVR).Update(ctx, updatedAppliedManifestWork, metav1.UpdateOptions{})
		if apiGone(err) {
			continue
		}
		if err != nil {
			return stepResult{}, fmt.Errorf("clear AppliedManifestWork %q finalizers: %w", ownerReference.Name, err)
		}
		return stepResult{
			done:   false,
			reason: "cleared AppliedManifestWork finalizers; verifying on next reconcile",
			values: []any{"appliedmanifestwork", ownerReference.Name},
		}, nil
	}

	return stepResult{done: true}, nil
}

// ensureDanglingAppliedManifestWorkOwnerRefsRemoved removes AppliedManifestWork
// ownerReferences. Earlier steps orphan the ManifestWork and clear
// AppliedManifestWork finalizers; this step only strips the ownerReferences.
// Runs last so those steps are not blocked.
func (c *OwnershipCleanupController) ensureDanglingAppliedManifestWorkOwnerRefsRemoved(ctx context.Context, ownedResource *ownershipResource) (stepResult, error) {
	oldOwnerReferences := ownedResource.obj.GetOwnerReferences()
	newOwnerReferences := make([]metav1.OwnerReference, 0, len(oldOwnerReferences))
	removed := false
	for _, ownerReference := range oldOwnerReferences {
		if ownerReference.Kind == "AppliedManifestWork" && len(ownerReference.Name) > 0 {
			removed = true
			continue
		}
		newOwnerReferences = append(newOwnerReferences, ownerReference)
	}
	if !removed {
		return stepResult{done: true}, nil
	}

	if err := c.updateOwnerReferences(ctx, ownedResource, newOwnerReferences); err != nil {
		return stepResult{}, err
	}
	return stepResult{
		done:   false,
		reason: "removed AppliedManifestWork ownerReferences; verifying on next reconcile",
		values: []any{"oldOwnerReferences", oldOwnerReferences, "newOwnerReferences", newOwnerReferences},
	}, nil
}

func appliedManifestWorkOwnerReferences(obj metav1.Object) []metav1.OwnerReference {
	seenNames := map[string]struct{}{}
	ownerReferences := make([]metav1.OwnerReference, 0)
	for _, ownerReference := range obj.GetOwnerReferences() {
		if ownerReference.Kind != "AppliedManifestWork" || len(ownerReference.Name) == 0 {
			continue
		}
		if _, alreadySeen := seenNames[ownerReference.Name]; alreadySeen {
			continue
		}
		seenNames[ownerReference.Name] = struct{}{}
		ownerReferences = append(ownerReferences, ownerReference)
	}
	return ownerReferences
}

func isACMOwnerReference(ref metav1.OwnerReference) bool {
	if ref.Kind == "AppliedManifestWork" {
		return true
	}
	gv, err := schema.ParseGroupVersion(ref.APIVersion)
	if err != nil {
		return false
	}
	return stringsHasOCMSuffix(gv.Group)
}

func (c *OwnershipCleanupController) updateOwnerReferences(ctx context.Context, ownedResource *ownershipResource, remainingOwnerReferences []metav1.OwnerReference) error {
	logger := utils.LoggerFromContext(ctx).WithValues(
		"resource", objectRef(&ownedResource.obj),
		"gvr", ownedResource.gvr.String(),
	)
	updatedResource := ownedResource.obj.DeepCopy()
	updatedResource.SetOwnerReferences(remainingOwnerReferences)
	logger.Info("removing dangling AppliedManifestWork ownerReferences")

	var err error
	if len(updatedResource.GetNamespace()) == 0 {
		_, err = c.dynamicClient.Resource(ownedResource.gvr).Update(ctx, updatedResource, metav1.UpdateOptions{})
	} else {
		_, err = c.dynamicClient.Resource(ownedResource.gvr).Namespace(updatedResource.GetNamespace()).Update(ctx, updatedResource, metav1.UpdateOptions{})
	}
	if apiGone(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("update ownerReferences on %s: %w", objectRef(&ownedResource.obj), err)
	}
	return nil
}

func (c *OwnershipCleanupController) getAppliedManifestWork(ctx context.Context, appliedManifestWorkName string) (*unstructured.Unstructured, error) {
	appliedManifestWork, err := c.dynamicClient.Resource(AppliedManifestWorksGVR).Get(ctx, appliedManifestWorkName, metav1.GetOptions{})
	if apiGone(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get AppliedManifestWork %q: %w", appliedManifestWorkName, err)
	}
	return appliedManifestWork, nil
}

// findManifestWork locates the ManifestWork for an AppliedManifestWork ownerRef.
// Prefer spec.manifestWorkName from the AppliedManifestWork when present;
// otherwise match ManifestWorks whose name is a suffix of the AppliedManifestWork
// name ({hubHash}-{manifestWorkName}).
func (c *OwnershipCleanupController) findManifestWork(ctx context.Context, appliedManifestWorkName, manifestWorkName string) (*unstructured.Unstructured, error) {
	manifestWorks, err := listNamespaced(ctx, c.dynamicClient, ManifestWorksGVR, metav1.NamespaceAll)
	if err != nil {
		return nil, err
	}
	for i := range manifestWorks {
		manifestWork := &manifestWorks[i]
		if len(manifestWorkName) > 0 && manifestWork.GetName() == manifestWorkName {
			return manifestWork, nil
		}
		if len(manifestWorkName) == 0 && manifestWorkMatchesAMWName(appliedManifestWorkName, manifestWork.GetName()) {
			return manifestWork, nil
		}
	}
	return nil, nil
}

func manifestWorkMatchesAMWName(appliedManifestWorkName, manifestWorkName string) bool {
	if len(manifestWorkName) == 0 {
		return false
	}
	nameSuffix := "-" + manifestWorkName
	return strings.HasSuffix(appliedManifestWorkName, nameSuffix) && len(appliedManifestWorkName) > len(nameSuffix)
}

func manifestWorkPropagationPolicy(manifestWork *unstructured.Unstructured) string {
	propagationPolicy, _, _ := unstructured.NestedString(manifestWork.Object, "spec", "deleteOption", "propagationPolicy")
	return propagationPolicy
}

// ownershipResource is a listed object plus the GVR used to update it.
type ownershipResource struct {
	gvr schema.GroupVersionResource
	obj unstructured.Unstructured
}

// listOwnershipCleanupCandidates returns HostedCluster-related ownership
// resources plus cluster-scoped PodNetworks and namespaced PodNetworkInstances.
func listOwnershipCleanupCandidates(ctx context.Context, dynamicClient dynamic.Interface) ([]ownershipResource, error) {
	resources, err := listHostedClusterOwnershipResources(ctx, dynamicClient)
	if err != nil {
		return nil, err
	}

	podNetworks, err := listClusterScoped(ctx, dynamicClient, PodNetworksGVR)
	if err != nil {
		return nil, err
	}
	for i := range podNetworks {
		resources = append(resources, ownershipResource{gvr: PodNetworksGVR, obj: podNetworks[i]})
	}

	podNetworkInstances, err := listNamespaced(ctx, dynamicClient, PodNetworkInstancesGVR, metav1.NamespaceAll)
	if err != nil {
		return nil, err
	}
	for i := range podNetworkInstances {
		resources = append(resources, ownershipResource{gvr: PodNetworkInstancesGVR, obj: podNetworkInstances[i]})
	}
	return resources, nil
}

func listHostedClusterOwnershipResources(ctx context.Context, dynamicClient dynamic.Interface) ([]ownershipResource, error) {
	namespacesByName, err := namespacesByName(ctx, dynamicClient)
	if err != nil {
		return nil, err
	}

	hostedClusters, err := listNamespaced(ctx, dynamicClient, HostedClustersGVR, metav1.NamespaceAll)
	if err != nil {
		return nil, err
	}

	hcNamespaceGVRs := []schema.GroupVersionResource{
		NodePoolsGVR,
		ConfigMapsGVR,
		SecretsGVR,
		SecretSyncsGVR,
		SecretProviderClassesGVR,
	}
	resources := make([]ownershipResource, 0, len(hostedClusters))
	for i := range hostedClusters {
		hostedCluster := &hostedClusters[i]
		resources = append(resources, ownershipResource{gvr: HostedClustersGVR, obj: *hostedCluster})

		if hostedClusterNamespace, ok := namespacesByName[hostedCluster.GetNamespace()]; ok {
			resources = append(resources, ownershipResource{
				gvr: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"},
				obj: *hostedClusterNamespace,
			})
		}

		namespacedResources, err := listInNamespace(ctx, dynamicClient, hostedCluster.GetNamespace(), hcNamespaceGVRs)
		if err != nil {
			return nil, fmt.Errorf("list HostedCluster namespace %q: %w", hostedCluster.GetNamespace(), err)
		}
		resources = append(resources, namespacedResources...)

		hostedControlPlaneNamespaceName := hostedCluster.GetNamespace() + "-" + hostedCluster.GetName()
		if hostedControlPlaneNamespace, ok := namespacesByName[hostedControlPlaneNamespaceName]; ok {
			resources = append(resources, ownershipResource{
				gvr: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"},
				obj: *hostedControlPlaneNamespace,
			})
		}
	}
	return resources, nil
}

func listInNamespace(ctx context.Context, dynamicClient dynamic.Interface, namespace string, gvrs []schema.GroupVersionResource) ([]ownershipResource, error) {
	resources := make([]ownershipResource, 0)
	for _, gvr := range gvrs {
		namespacedItems, err := listNamespaced(ctx, dynamicClient, gvr, namespace)
		if err != nil {
			return nil, err
		}
		for i := range namespacedItems {
			resources = append(resources, ownershipResource{gvr: gvr, obj: namespacedItems[i]})
		}
	}
	return resources, nil
}

func namespacesByName(ctx context.Context, dynamicClient dynamic.Interface) (map[string]*unstructured.Unstructured, error) {
	namespaces, err := listClusterScoped(ctx, dynamicClient, schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"})
	if err != nil {
		return nil, err
	}
	namespacesByName := make(map[string]*unstructured.Unstructured, len(namespaces))
	for i := range namespaces {
		namespacesByName[namespaces[i].GetName()] = &namespaces[i]
	}
	return namespacesByName, nil
}

func listClusterScoped(ctx context.Context, dynamicClient dynamic.Interface, gvr schema.GroupVersionResource) ([]unstructured.Unstructured, error) {
	list, err := dynamicClient.Resource(gvr).List(ctx, metav1.ListOptions{})
	if apiGone(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", gvr.String(), err)
	}
	return list.Items, nil
}

func listNamespaced(ctx context.Context, dynamicClient dynamic.Interface, gvr schema.GroupVersionResource, namespace string) ([]unstructured.Unstructured, error) {
	list, err := dynamicClient.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if apiGone(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list %s in namespace %q: %w", gvr.String(), namespace, err)
	}
	return list.Items, nil
}
