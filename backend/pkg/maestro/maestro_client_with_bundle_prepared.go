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

package maestro

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	workv1client "open-cluster-management.io/api/client/work/clientset/versioned/typed/work/v1"
	workv1 "open-cluster-management.io/api/work/v1"
	manifestworkclientutils "open-cluster-management.io/sdk-go/pkg/cloudevents/clients/utils"

	kuberrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	readOnlyResourceJsonPathName string = "resource"
	statusOnlyJsonPathName       string = "status"
	ReadOnlyAnnotation           string = "maestro.readonly"
	resourceTypeLabel            string = "maestro.resource.type"
)

// MaestroClientWithBundlePrepared is a client that allows you to interact with maestro where:
//   - For Create, Update and Patch operations you provide a typed K8s Resource and it returns a Maestro Bundle with the resource within it
//   - For Get operations you provide an typed K8s Resource, its K8s Name and Namespace and it returns a Maestro Bundle with the resource within it
//   - For List operations you provide an empty typed K8s List Object and it returns a list of Maestro Bundles with each bundle with the corresponding resource within it
//
// The client takes care of generating a calculating the bundle name based on the provided typed K8s resource GVK, K8s Namespace and K8s Name
// and it uses a UUIDv5 to generate a unique bundle name. his means the consumer code does not have control on specifying particular bundle names
// directly.
// To support List and Delete operations which are based on the resource GVK we store a K8s Label on the Maestro Bundle once Create is called.
// This also creates the Maestro Bundle in a consistent way:
// * The Maestro Bundle name is always a UUIDv5 based on the provided typed K8s resource GVK, K8s Namespace and K8s Name
// * The StatusFeedback rules configured in the Maestro Bundle are always the same:
//   - For owned resources, the status feedback includes status section of the resource
//   - For readonly resources, the status feedback includes the whole resource
//
// * The UpdateStrategy of the Maestro Bundle is always configured in the same way:
//   - For owned resources, the update strategy is ServerSideApply, with the field manager set to "maestro-agent" and force set to true
//   - For readonly resources, the update strategy is ReadOnly
type MaestroClientWithBundlePrepared struct {
	maestroManifestWorksInterface workv1client.ManifestWorkInterface
	maestroConsumerName           string
	scheme                        *runtime.Scheme
}

// Maestro client is composed of:
// * REST HTTP client
// * GRPC client
// * Maestro offers an openclustermanagement ManifestWorksClient interface to interact transparently using those (although you have to construct them)
// * Additionally, with the provided ManifestWorksClient you can do a Watch on resources which is what we use to proactively receive Maestro events. We
//   use that to receive maestro events, process them and dispatch them to other K8s resource-specific reconcilers.

// TODO we need to instantiate Maestro client based on each cluster information. In CS we have the concept
// of Client Provider that is a factory of Maestro client that receives the cluster model because it's the one that
// contains the provision shard id associated to it.
// TODO we also need to instantiate Maestro client for the async controller that is responsible for processing Maestro
// bundles (we call this the Maestro watcher) and then dispatching to other reconcilers. We have a map of gvk(converted to uuid) to reconcilers.
// For each CS provision shard we have the same maestro client instantiated that is used in other parts of the code.
// We have concept of Shard Manager that keeps track of  mapping from provision shard it to the maestro client, as well
// as provision shard id and the provision shard model
// Additionally, we also have a "deletion" logic where when the shard is deleted we cleanup the associated mappings and client.
// The same occurs if the pod is not leader anymore.
// We also have a worker that periodically checks if there's been changes on the DB around shards because it needs to
// be aware of it to start considering new shards or some data has changed in existing ones.
// There's also shard reloading
// We have a "maestro"
// Shards can be registered in the shard inventory via API
// Any backend pod can be the leader and needs to be aware of changes
// Any backend stop that stops being the leader should stop being subscribed from Maestro. We need to make sure no two pods receive events and process them
// When a cluster is created it is allocated to a shard.
func NewMaestroClientWithBundlePrepared(maestroManifestWorksInterface workv1client.ManifestWorkInterface, scheme *runtime.Scheme) *MaestroClientWithBundlePrepared {
	return &MaestroClientWithBundlePrepared{

		maestroManifestWorksInterface: maestroManifestWorksInterface,
		scheme:                        scheme,
	}
}

// TODO should we abstract the Maestro bundle into its own type? it would help differentiate between the Maestro bundle and an ACM ManifestWork.
// Both rely on workv1.ManifestWork but the semantics are different
// TODO should we name it MaestroBundle, MaestroManifestBundle or MaestroResourceBundle?
func (c *MaestroClientWithBundlePrepared) CreateMaestroBundle(ctx context.Context, obj client.Object, opts ...client.CreateOption) (*workv1.ManifestWork, error) {
	createOpts := &client.CreateOptions{}
	createOpts.ApplyOptions(opts)
	desiredMaestroBundle, err := c.wrapK8sObjectInMaestroManifestBundle(obj)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to wrap k8s object in maestro manifest bundle: %w", err))
	}

	newMaestroBundle, err := c.maestroManifestWorksInterface.Create(ctx, desiredMaestroBundle, *createOpts.AsCreateOptions())
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create maestro bundle: %w", err))
	}

	return newMaestroBundle, nil
}

func (c *MaestroClientWithBundlePrepared) GetMaestroBundle(
	ctx context.Context,
	key client.ObjectKey,
	obj client.Object,
	opts ...client.GetOption,
) (*workv1.ManifestWork, error) {
	getOptions := &client.GetOptions{}
	getOptions.ApplyOptions(opts)

	// sets name and namespce as it is needed for the calculation
	// of the maestro manifest bundle name
	obj.SetName(key.Name)
	obj.SetNamespace(key.Namespace)

	name, err := c.generateUniqueMaestroManifestBundleName(obj)
	if err != nil {
		return nil, err
	}

	maestroBundle, err := c.maestroManifestWorksInterface.Get(ctx, name, *getOptions.AsGetOptions())
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get maestro bundle: %w", err))
	}

	return maestroBundle, nil
}

func (c *MaestroClientWithBundlePrepared) DeleteMaestroBundle(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	deleteOpts := &client.DeleteOptions{}
	deleteOpts.ApplyOptions(opts)

	name, err := c.generateUniqueMaestroManifestBundleName(obj)
	if err != nil {
		return err
	}
	err = c.maestroManifestWorksInterface.Delete(ctx, name, *deleteOpts.AsDeleteOptions())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to delete maestro bundle: %w", err))
	}
	return nil
}

func (c *MaestroClientWithBundlePrepared) UpdateMaestroBundle(ctx context.Context, obj client.Object, opts ...client.UpdateOption) (*workv1.ManifestWork, error) {
	updateOpts := &client.UpdateOptions{}
	updateOpts.ApplyOptions(opts)

	existingMaestroBundle, err := c.wrapK8sObjectInMaestroManifestBundle(obj)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to wrap k8s object in maestro manifest bundle: %w", err))
	}

	gvk, err := apiutil.GVKForObject(obj, c.scheme)
	if err != nil {
		return nil, err
	}

	if len(existingMaestroBundle.Spec.Workload.Manifests) != 1 {
		return nil, kuberrors.NewInvalid(gvk.GroupKind(), obj.GetName(),
			// intentionally left as empty list as the manifest bundle is invalid.
			field.ErrorList{},
		)
	}

	// update the manifest bundle with the patched object, annotations and lables
	existingMaestroBundle.Spec.Workload.Manifests[0] = workv1.Manifest{RawExtension: runtime.RawExtension{
		Object: obj,
	}}

	// TODO we haven't verified if we need to unset some attributes because of
	// Maestro not allowing it during Update call.
	// For now we keep it here:
	// unset the fields before patching maestro's manifest
	// We do this because maestro manifest bundle doesn't allow setting
	// these fields during maestro's manifest bundle update
	existingMaestroBundle.SetManagedFields(nil)
	existingMaestroBundle.SetUID("")
	existingMaestroBundle.SetResourceVersion("")
	existingMaestroBundle.SetOwnerReferences(nil)
	existingMaestroBundle.SetDeletionTimestamp(nil)
	existingMaestroBundle.SetCreationTimestamp(metav1.Time{})

	// We set the object K8s labels and K8s annotations in the maestro bundle
	labels := map[string]string{}
	for k, v := range obj.GetLabels() {
		labels[k] = v
	}
	// this label will be used for filtering by type when listing resources
	labels[resourceTypeLabel] = c.gvkToUUID(gvk)
	existingMaestroBundle.SetLabels(labels)
	existingMaestroBundle.SetAnnotations(obj.GetAnnotations())

	res, err := c.maestroManifestWorksInterface.Update(ctx, existingMaestroBundle, *updateOpts.AsUpdateOptions())
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to update maestro bundle: %w", err))
	}

	return res, nil
}

// PatchMaestroBundle patches the base obj by applying the patch.
// The method only supports JSONPatch and MergePatch as those are the only ones supported by
// maestro
//
// The operations for JSONPatch must adhere to the RFC-6902 specification.
// Note that in most cases, the specified target or from location must exist for the operation to succeed.
// See https://datatracker.ietf.org/doc/html/rfc6902#section-4 for reference.
//
// For MergePatch, see https://datatracker.ietf.org/doc/html/rfc7396 for reference.
func (c *MaestroClientWithBundlePrepared) PatchMaestroBundle(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) (*workv1.ManifestWork, error) {
	patchOpts := &client.PatchOptions{}
	patchOpts.ApplyOptions(opts)

	maestroBundleName, err := c.generateUniqueMaestroManifestBundleName(obj)
	if err != nil {
		return nil, err
	}
	existingMaestroBundle, err := c.maestroManifestWorksInterface.Get(ctx, maestroBundleName, metav1.GetOptions{})
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get existing maestro bundle: %w", err))
	}

	gvk, err := apiutil.GVKForObject(obj, c.scheme)
	if err != nil {
		return nil, err
	}

	if len(existingMaestroBundle.Spec.Workload.Manifests) != 1 {

		return nil, kuberrors.NewInvalid(gvk.GroupKind(), obj.GetName(),
			// intentionally left as empty list as the manifest bundle is invalid.
			field.ErrorList{},
		)
	}

	// patch the existing object wrapped in the manifest bundle
	existingObjectBytes, err := existingMaestroBundle.Spec.Workload.Manifests[0].MarshalJSON()
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to marshal existing object: %w", err))
	}

	existingObjectAsUnstructured := &unstructured.Unstructured{}
	err = json.Unmarshal(existingObjectBytes, existingObjectAsUnstructured)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to unmarshal existing object as unstructured: %w", err))
	}

	patchData, err := patch.Data(obj)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get patch data: %w", err))
	}
	patchedObj, err := manifestworkclientutils.Patch(
		patch.Type(),
		existingObjectAsUnstructured,
		patchData,
	)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to apply patch data to existing object: %w", err))
	}

	// unset the fields before patching maestro's manifest
	// We do this because maestro manifest bundle doesn't allow setting
	// these fields during maestro's manifest bundle update
	patchedObj.SetManagedFields(nil)
	patchedObj.SetUID("")
	patchedObj.SetResourceVersion("")
	patchedObj.SetOwnerReferences(nil)
	patchedObj.SetDeletionTimestamp(nil)
	patchedObj.SetCreationTimestamp(metav1.Time{})

	// update the manifest bundle with the patched object, annotations and lables
	existingMaestroBundle.Spec.Workload.Manifests[0] = workv1.Manifest{RawExtension: runtime.RawExtension{
		Object: patchedObj,
	}}

	labels := map[string]string{}
	for k, v := range patchedObj.GetLabels() {
		labels[k] = v
	}
	// this label will be used for filtering by type when listing resources
	labels[resourceTypeLabel] = c.gvkToUUID(gvk)
	existingMaestroBundle.SetLabels(labels)
	existingMaestroBundle.SetAnnotations(patchedObj.GetAnnotations())

	// create patch object that replaces bundle's spec and metadata fields
	specBytes, err := json.Marshal(existingMaestroBundle.Spec)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to marshal existing maestro bundle spec: %w", err))
	}
	specOp := fmt.Sprintf(`{"op":"replace","path":"/spec","value":%s}`,
		string(specBytes))

	metadataBytes, err := json.Marshal(existingMaestroBundle.ObjectMeta)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to marshal existing maestro bundle metadata: %w", err))
	}
	metadataOp := fmt.Sprintf(`{"op":"replace","path":"/metadata","value":%s}`,
		string(metadataBytes))

	patchOptions := &client.PatchOptions{}
	patchOptions.ApplyOptions(opts)

	res, err := c.maestroManifestWorksInterface.Patch(ctx, maestroBundleName, types.JSONPatchType,
		[]byte(fmt.Sprintf("[%s,%s]", specOp, metadataOp)),
		*patchOptions.AsPatchOptions(),
	)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to patch maestro bundle: %w", err))
	}

	return res, nil
}

// ListMaestroBundles lists the maestro bundles for the given list object.
// The list object is expected to be a typed K8s List Object.
// Internally we use the GVK to filter by a label that we set during MaestroBundle Create
func (c *MaestroClientWithBundlePrepared) ListMaestroBundles(ctx context.Context, list client.ObjectList, opts ...client.ListOption) (*workv1.ManifestWorkList, error) {
	listOpts := &client.ListOptions{}
	listOpts.ApplyOptions(opts)

	gvk, err := apiutil.GVKForObject(list, c.scheme)
	if err != nil {
		return nil, err
	}

	gvk.Kind = strings.TrimSuffix(gvk.Kind, "List")
	// lean on the safe side and ensures that the new gvk exists
	_, err = c.scheme.New(gvk)
	if err != nil {
		return nil, err
	}
	// search maestro bundles by resource type (GVK)
	labels := map[string]string{
		resourceTypeLabel: c.gvkToUUID(gvk),
	}

	listOptions := &client.ListOptions{}
	opts = append(opts, client.MatchingLabels(labels))
	listOptions.ApplyOptions(opts)

	// TODO handling of size and pagination to make the implementation complete
	maestroBundlesList, err := c.maestroManifestWorksInterface.List(ctx, *listOptions.AsListOptions())
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to list maestro bundles: %w", err))
	}

	return maestroBundlesList, nil
}

// wrapsK8sObjectInMaestroManifestBundle wraps the received object onto a Maestro's manifest bundle
// for delivery onto the spoke cluster identified by this client's namespace
func (c *MaestroClientWithBundlePrepared) wrapK8sObjectInMaestroManifestBundle(obj client.Object) (*workv1.ManifestWork, error) {
	// when retreiving the existing object on target cluster, the whole object will be
	// on the status and  parsed in one go
	readOnlyJsonPath := workv1.JsonPath{
		Name: readOnlyResourceJsonPathName,
		Path: "@",
	}

	// in case of k8s resources created by CS via Maestro, CS own the manifest which is the spec
	// and Maestro own the status.
	// parsing of the object will happen in two steps;
	// 1. Parse the manifest first i.e the spec owned by CS
	// 2. Retrieve the status feedback result and parse it and append it
	// to the result above to get a complete object
	statusOnlyJsonPath := workv1.JsonPath{
		Name: statusOnlyJsonPathName,
		Path: ".status",
	}

	name, err := c.generateUniqueMaestroManifestBundleName(obj)
	if err != nil {
		return nil, err
	}
	gvk, err := apiutil.GVKForObject(obj, c.scheme)
	if err != nil {
		return nil, err
	}
	manifestUpdateStrategy := &workv1.UpdateStrategy{
		Type: workv1.UpdateStrategyTypeServerSideApply,
		ServerSideApply: &workv1.ServerSideApplyConfig{
			// We define that if there are multiple competing actors on the ManifestWork
			// maestro agent has preference.
			Force:        true,
			FieldManager: "maestro-agent",
		},
	}
	statusFeedbackJsonPath := statusOnlyJsonPath
	// its enough to check for existence of this annotation
	if _, ok := obj.GetAnnotations()[ReadOnlyAnnotation]; ok {
		// it's a readonly object, use the readonly strategy to only check the existence
		// of the resource based on the resource's metadata.
		// The resulting object will be on statusFeedback and .Get(ctx,key, obj)
		// can be used to retrieve it afterwards
		manifestUpdateStrategy.Type = workv1.UpdateStrategyTypeReadOnly
		statusFeedbackJsonPath = readOnlyJsonPath
	}

	// manifest bundle labels
	labels := map[string]string{}
	for k, v := range obj.GetLabels() {
		labels[k] = v
	}

	// this label will be used for filtering by type when listing resources
	labels[resourceTypeLabel] = c.gvkToUUID(gvk)

	return &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       c.maestroConsumerName,
			ResourceVersion: "0",
			Annotations:     obj.GetAnnotations(),
			Labels:          labels,
		},
		Spec: workv1.ManifestWorkSpec{
			Workload: workv1.ManifestsTemplate{
				Manifests: []workv1.Manifest{
					{
						RawExtension: runtime.RawExtension{
							Object: obj,
						},
					},
				},
			},
			ManifestConfigs: []workv1.ManifestConfigOption{
				{
					ResourceIdentifier: workv1.ResourceIdentifier{
						Group:     gvk.Group,
						Resource:  strings.ToLower(gvk.Kind) + "s", // TODO confirm if this conversion is okay
						Name:      obj.GetName(),
						Namespace: obj.GetNamespace(),
					},
					UpdateStrategy: manifestUpdateStrategy,
					FeedbackRules: []workv1.FeedbackRule{
						{
							Type: workv1.JSONPathsType,
							JsonPaths: []workv1.JsonPath{
								statusFeedbackJsonPath,
							},
						},
					},
				},
			},
		},
	}, nil
}

// generateUniqueMaestroManifestBundleName generates a unique maestro's manifest bundle name by
// generating a namespace uuid from the concatenation of the obj name, obj namespace and its group version kind
// We want predicatable generation of name based on input hence the usage of uuid v5
// We want it to be less than 64 chars.
// If the manifest bundle is deleted and recreated again with the same name, it shouldn't be an issue
// because maestro hard deletes the resource
func (c *MaestroClientWithBundlePrepared) generateUniqueMaestroManifestBundleName(obj client.Object) (string, error) {
	gvk, err := apiutil.GVKForObject(obj, c.scheme)
	if err != nil {
		return "", err
	}

	nameWithGvk := obj.GetName() + "-" + obj.GetNamespace() + "-" + gvk.String()
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(nameWithGvk)).String(), nil
}

// gvkToUuid transforms the GroupVersionKind to an uuid that's a valid
// k8s label value
// We want predicatable generation of the label based on input hence the usage of uuid v5
// We want it to be less than 64 chars.
func (c *MaestroClientWithBundlePrepared) gvkToUUID(gvk schema.GroupVersionKind) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(gvk.String())).String()
}
