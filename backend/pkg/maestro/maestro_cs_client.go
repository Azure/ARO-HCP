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
	"net/http"
	"strings"

	"github.com/google/uuid"
	v1 "open-cluster-management.io/api/client/work/clientset/versioned/typed/work/v1"
	workv1 "open-cluster-management.io/api/work/v1"
	manifestworkclientutils "open-cluster-management.io/sdk-go/pkg/cloudevents/clients/utils"
	sdkgologging "open-cluster-management.io/sdk-go/pkg/logging"

	kuberrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	sdklogging "github.com/openshift-online/ocm-sdk-go/logging"
)

// The type alias is done so that we can mock the external dependency and use it
// in our unit tests
//
// nolint:unused
//
//IGNORE go:generate mockgen -source=maestro_client.go -package=maestro -destination=mock_manifest_work_interface.go
// type maestroManifestBundleClient interface {
// 	v1.ManifestWorkInterface
// }

// maestroCSClient is a client that wraps the Maestro API and provides a simple interface for creating, getting, deleting, updating and listing Maestro bundles.
// The relevant design around it is that it is compatible with some signatures in K8s controller-runtime and lets consumer code
// use it as a regular K8s controller-runtime client. Specifically, it
// implements the following interface:
//
//	type anInterface interface {
//		client.Writer // k8s controller-runtime client.Writer interface
//		client.Reader // k8s controller-runtime client.Reader interface
//		Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error // k8s controller-runtime client.Apply interface
//
// It hides the awareness from the consumer code of this being a Maestro client and the concepts of Maestro bundles.
// The main reason this was performed is because in CS we had the codebase shared between different offerings, not only
// ARO-HCP, and many of the code paths were shared. By making it compatible with the K8s controller-runtime client interface
// we could still reuse the same code paths and interfaces for both offerings.
// Internally, the client implementation what does is it builds the Maestro Bundle and it
// puts within it the K8s object to be created, which can be a common K8s object, including an ACM ManifestWork itself.
// Although this worked, it turned out to be problematic in some areas:
//   - Although it is compatible with the K8s controller-runtime client, communicating
//     with Maestro API is not the same as communicating with the K8s API. For example, in Maestro
//     the creation of a resource takes some time to propagate to the destination K8s cluster due to to the asynchronous
//     nature of it, or it can even fail somewhere along the way. This is different than
//     the K8s API where when the K8s API request is accepted you have a guarantee that it is at least stored in the
//     K8s cluster etcd database
//   - Because of the client implementation hiding the concept of the Maestro bundle, it is not possible to
//     look at the status of the Maestro bundle itself from the code that consumes the Maestro client. This has
//     turned out to be problematic because it does not allow to directly determine from consumer code whether the Maestro bundle
//     has been applied. As you will see, here we try to somewhat internally determine some cases from within the client implementation
//     and if it's not applied we return as if it didn't exist. However, that does not solve all edge cases and ends up with
//     an awkward design.
//
// Another decision that was taken was that we would generate the Maestro bundle names based on the K8s object being
// created. Specifically, based on the Name, Namespace and GVK of it and then generating a UUIDv5 based on those. This
// allowed us to let the consumer code again act as if it was interacting with a regular K8s API. Using UUIDv5 also
// allowed us to not store/track the Maestro bundle names. However, this maybe turned out to be a bit fragile because
// of being hard to change in the future if needed
// Another decision was that because we wanted the consumre code to use it as a K8s client we allowed one and only one
// K8s object per Maestro Bundle. To put multiple objects within a single Maestro Bundle consumer code can provide
// an ACM ManifestWork, which we do in several cases like for example HostedCluster CR and associated resources to it,
// part of the reason being that this was the way it was created in other offerings and we were reusing code.
type maestroCSClient struct {
	namespace string
	scheme    *runtime.Scheme
	client    v1.ManifestWorkInterface
	logger    sdklogging.Logger
}

func wrapMaestroError(err error) error {
	if err != nil {
		return fmt.Errorf("maestro error: %w", err)
	}
	return nil
}

func NewMaestroClient(
	namespace string,
	scheme *runtime.Scheme,
	logger sdklogging.Logger,
	client v1.ManifestWorkInterface) *maestroCSClient {

	return &maestroCSClient{
		client:    client,
		scheme:    scheme,
		logger:    logger,
		namespace: namespace,
	}
}

// enrichContextWithOperationId adds the operation ID to the context using the key
// expected by Maestro for end-to-end tracing.
// Uses the standard Open Cluster Management SDK context tracing key for operation IDs.
func (c *maestroCSClient) enrichContextWithOperationId(ctx context.Context) context.Context {
	// In CS there's the concept of operation ID that identifies an operation to be
	// able to trace the operation and we send it to Maestro for cross-service tracing
	// and we extract it from the context.
	// This implementation here just changes that for a random UUID as we don't have that same operation ID
	// in the Backend.
	// TODO figure out what to do around this in backend
	operationID := uuid.New().String()
	return context.WithValue(ctx, sdkgologging.ContextTracingOPIDKey, operationID)
}

func (c *maestroCSClient) Create(
	ctx context.Context,
	obj client.Object,
	opts ...client.CreateOption) error {

	createOpts := &client.CreateOptions{}
	createOpts.ApplyOptions(opts)
	mw, err := c.wrapsK8sObjectInMaestroManifestBundle(obj)
	if err != nil {
		return err
	}
	_, err = c.client.Create(c.enrichContextWithOperationId(ctx), mw, *createOpts.AsCreateOptions())
	return wrapMaestroError(err)
}

// Patch patches the base obj by applying the patch.
// The method only supports JSONPatch and MergePatch as those are the only ones supported by
// maestro
//
// The operations for JSONPatch must adhere to the RFC-6902 specification.
// Note that in most cases, the specified target or from location must exist for the operation to succeed.
// See https://datatracker.ietf.org/doc/html/rfc6902#section-4 for reference.
//
// For MergePatch, see https://datatracker.ietf.org/doc/html/rfc7396 for reference.
func (c *maestroCSClient) Patch(
	ctx context.Context,
	obj client.Object,
	patch client.Patch,
	opts ...client.PatchOption) error {
	patchData, err := patch.Data(obj)
	if err != nil {
		return err
	}
	name, err := c.generateUniqueMaestroManifestBundleName(obj)
	if err != nil {
		return err
	}
	enrichedCtx := c.enrichContextWithOperationId(ctx)
	mw, err := c.client.Get(enrichedCtx, name, metav1.GetOptions{})
	if err != nil {
		return wrapMaestroError(err)
	}

	gvk, err := apiutil.GVKForObject(obj, c.scheme)
	if err != nil {
		return err
	}

	if len(mw.Spec.Workload.Manifests) != 1 {
		return kuberrors.NewInvalid(gvk.GroupKind(), obj.GetName(),
			// intentionally left as empty list as the manifest bundle is invalid.
			field.ErrorList{},
		)
	}

	// patch the existing object wrapped in the manifest bundle
	existingObjectBytes, err := mw.Spec.Workload.Manifests[0].MarshalJSON()
	if err != nil {
		return err
	}

	existingObjectAsUnstructured := &unstructured.Unstructured{}
	err = json.Unmarshal(existingObjectBytes, existingObjectAsUnstructured)
	if err != nil {
		return err
	}
	patchedObj, err := manifestworkclientutils.Patch(
		patch.Type(),
		existingObjectAsUnstructured,
		patchData)
	if err != nil {
		return err
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
	mw.Spec.Workload.Manifests[0] = workv1.Manifest{RawExtension: runtime.RawExtension{
		Object: patchedObj,
	}}

	labels := map[string]string{}
	for k, v := range patchedObj.GetLabels() {
		labels[k] = v
	}
	// this label will be used for filtering by type when listing resources
	labels[resourceTypeLabel] = c.gvkToUuid(gvk)
	mw.SetLabels(labels)
	mw.SetAnnotations(patchedObj.GetAnnotations())

	// create patch object that replaces bundle's spec and metadata fields
	specBytes, err := json.Marshal(mw.Spec)
	if err != nil {
		return err
	}
	specOp := fmt.Sprintf(`{"op":"replace","path":"/spec","value":%s}`,
		string(specBytes))

	metadataBytes, err := json.Marshal(mw.ObjectMeta)
	if err != nil {
		return err
	}
	metadataOp := fmt.Sprintf(`{"op":"replace","path":"/metadata","value":%s}`,
		string(metadataBytes))

	patchOptions := &client.PatchOptions{}
	patchOptions.ApplyOptions(opts)

	_, err = c.client.Patch(enrichedCtx, name, types.JSONPatchType,
		[]byte(fmt.Sprintf("[%s,%s]", specOp, metadataOp)),
		*patchOptions.AsPatchOptions(),
	)
	return wrapMaestroError(err)
}

// Apply implements the client.Client interface. This method is not supported for Maestro
// as it uses ManifestWorks which don't support server-side apply semantics.
func (c *maestroCSClient) Apply(
	ctx context.Context,
	obj runtime.ApplyConfiguration,
	opts ...client.ApplyOption) error {
	return fmt.Errorf("Apply is not supported y the mastro client")
}

func (c *maestroCSClient) Update(
	ctx context.Context,
	obj client.Object,
	opts ...client.UpdateOption) error {
	name, err := c.generateUniqueMaestroManifestBundleName(obj)
	if err != nil {
		return err
	}

	enrichedCtx := c.enrichContextWithOperationId(ctx)
	mw, err := c.client.Get(enrichedCtx, name, metav1.GetOptions{})
	if err != nil {
		return wrapMaestroError(err)
	}

	if mw.GetResourceVersion() != obj.GetResourceVersion() {
		return &kuberrors.StatusError{
			ErrStatus: metav1.Status{
				Status: metav1.StatusFailure,
				Code:   http.StatusConflict,
				Reason: metav1.StatusReasonConflict,
				Message: fmt.Sprintf("'%s' version '%s' doesn't match current version '%s'",
					obj.GetName(),
					obj.GetResourceVersion(),
					mw.GetResourceVersion(),
				),
			},
		}
	}

	// unset the fields before patching maestro's manifest
	// We do this because maestro manifest bundle doesn't allow setting
	// these fields during maestro's manifest bundle update
	obj.SetManagedFields(nil)
	obj.SetUID("")
	obj.SetResourceVersion("")
	obj.SetOwnerReferences(nil)
	obj.SetDeletionTimestamp(nil)
	obj.SetCreationTimestamp(metav1.Time{})

	// update the manifest bundle
	mw.Spec.Workload.Manifests[0] = workv1.Manifest{
		RawExtension: runtime.RawExtension{
			Object: obj,
		}}

	mw.SetAnnotations(obj.GetAnnotations())

	labels := map[string]string{}
	for k, v := range obj.GetLabels() {
		labels[k] = v
	}
	// this label will be used for filtering by type when listing resources
	gvk, err := apiutil.GVKForObject(obj, c.scheme)
	if err != nil {
		return err
	}
	labels[resourceTypeLabel] = c.gvkToUuid(gvk)
	mw.SetLabels(labels)

	// create patch object that replaces bundle's spec and metadata fields
	specBytes, err := json.Marshal(mw.Spec)
	if err != nil {
		return err
	}
	specOp := fmt.Sprintf(`{"op":"replace","path":"/spec", "value": %s}`,
		string(specBytes))

	metadataBytes, err := json.Marshal(mw.ObjectMeta)
	if err != nil {
		return err
	}
	metadataOp := fmt.Sprintf(`{"op":"replace","path":"/metadata", "value": %s}`,
		string(metadataBytes))
	patch := fmt.Sprintf("[%s,%s]", specOp, metadataOp)

	// patch the corresponding manifest bundle with an update request of the object
	updateOptions := &client.UpdateOptions{}
	updateOptions.ApplyOptions(opts)
	rawUpdateOptions := *updateOptions.AsUpdateOptions()
	_, err = c.client.Patch(enrichedCtx,
		name,
		types.JSONPatchType,
		[]byte(patch),
		metav1.PatchOptions{
			TypeMeta:        rawUpdateOptions.TypeMeta,
			DryRun:          rawUpdateOptions.DryRun,
			FieldManager:    rawUpdateOptions.FieldManager,
			FieldValidation: rawUpdateOptions.FieldValidation,
		},
	)
	return wrapMaestroError(err)
}

func (c *maestroCSClient) Delete(
	ctx context.Context,
	obj client.Object,
	opts ...client.DeleteOption) error {
	deleteOpts := &client.DeleteOptions{}
	deleteOpts.ApplyOptions(opts)
	name, err := c.generateUniqueMaestroManifestBundleName(obj)
	if err != nil {
		return err
	}
	err = c.client.Delete(c.enrichContextWithOperationId(ctx), name, *deleteOpts.AsDeleteOptions())
	return wrapMaestroError(err)
}

func (c *maestroCSClient) DeleteAllOf(
	ctx context.Context,
	obj client.Object,
	opts ...client.DeleteAllOfOption) error {

	gvk, err := apiutil.GVKForObject(obj, c.scheme)
	if err != nil {
		return err
	}

	// search by manifest bundle resource type
	labels := map[string]string{
		resourceTypeLabel: c.gvkToUuid(gvk),
	}
	opts = append(opts, client.MatchingLabels(labels))
	deleteAllOfOpts := &client.DeleteAllOfOptions{}
	deleteAllOfOpts.ApplyOptions(opts)

	enrichedCtx := c.enrichContextWithOperationId(ctx)
	// list all manifest bundle matching the resource type and
	// passed in list options
	// TODO handling of size and pagination
	mwList, err := c.client.List(enrichedCtx, *deleteAllOfOpts.AsListOptions())
	if err != nil {
		return wrapMaestroError(err)
	}

	// delete all found items
	for _, item := range mwList.Items {
		err = c.client.Delete(enrichedCtx, item.Name, *deleteAllOfOpts.AsDeleteOptions())
		if err != nil {
			return wrapMaestroError(err)
		}
	}

	return nil
}

func (c *maestroCSClient) List(
	ctx context.Context,
	list client.ObjectList,
	opts ...client.ListOption) error {

	gvk, err := apiutil.GVKForObject(list, c.scheme)
	if err != nil {
		return err
	}

	listKind := gvk.Kind
	gvk.Kind = strings.TrimSuffix(gvk.Kind, "List")

	// lean on the safe side and ensures that the new gvk exists
	_, err = c.scheme.New(gvk)
	if err != nil {
		return err
	}

	// search by manifest bundle resource type
	labels := map[string]string{
		resourceTypeLabel: c.gvkToUuid(gvk),
	}

	listOptions := &client.ListOptions{}
	opts = append(opts, client.MatchingLabels(labels))
	listOptions.ApplyOptions(opts)

	// TODO handling of size and pagination to make the implementation complete
	mwList, err := c.client.List(c.enrichContextWithOperationId(ctx), *listOptions.AsListOptions())
	if err != nil {
		return wrapMaestroError(err)
	}

	rawItems := []*unstructured.Unstructured{}

	// convert the manifest bundles onto a list of unstructured objects of
	// requested k8s objects
	for _, item := range mwList.Items {
		mw := item
		rawObject, exists := c.unwrapsRawObjectFromManifestBundle(ctx, c.logger, &mw)
		if !exists {
			continue
		}

		// unmarshal the object onto an unstructed object to be able to set
		// the resource version.
		u := &unstructured.Unstructured{}
		err = json.Unmarshal([]byte(rawObject), u)
		if err != nil { // should never happen
			return err
		}
		// sets the resource version from manifest work resource version
		// The version will be passed back when making an update request.
		// the resource version is a system generated value.
		// However for non readonly, no; We retrieve the spec i.e spec.WorkLoad.Manifests[0] part
		// and combine it with the status. The .ResourceVersion is part of the .ObjectMeta in the spec
		// and CS doesn't have it - it is a system generated value; in this case Maestro's generated version.
		// That's why we use the version associated with the bundle.
		u.SetResourceVersion(mw.GetResourceVersion())

		// set creation/deletion timestamps from Maestro's system generated values
		u.SetDeletionTimestamp(mw.GetDeletionTimestamp())
		u.SetCreationTimestamp(mw.GetCreationTimestamp())
		rawItems = append(rawItems, u)
	}

	// transform to requested list object
	rawItemsBytes, err := json.Marshal(rawItems)
	if err != nil {
		return err
	}

	m := fmt.Sprintf(`{"apiVersion": "%s", "kind": "%s" ,"items":%s}`,
		gvk.Version, listKind, string(rawItemsBytes))
	return json.Unmarshal([]byte(m), list)

}

func (c *maestroCSClient) Get(
	ctx context.Context,
	key client.ObjectKey,
	obj client.Object,
	opts ...client.GetOption) error {
	getOptions := &client.GetOptions{}
	getOptions.ApplyOptions(opts)

	// sets name and namespce as it is needed for the calculation
	// of the maestro manifest bundle name
	obj.SetName(key.Name)
	obj.SetNamespace(key.Namespace)

	name, err := c.generateUniqueMaestroManifestBundleName(obj)
	if err != nil {
		return err
	}

	manifestWork, err := c.client.Get(c.enrichContextWithOperationId(ctx), name, *getOptions.AsGetOptions())
	if err != nil {
		return wrapMaestroError(err)
	}

	statusJsonRaw, exists := c.unwrapsRawObjectFromManifestBundle(ctx, c.logger, manifestWork)
	if !exists {
		return &kuberrors.StatusError{
			ErrStatus: metav1.Status{
				Status:  metav1.StatusFailure,
				Code:    http.StatusNotFound,
				Reason:  metav1.StatusReasonNotFound,
				Message: fmt.Sprintf("%q not found", key.Name),
			},
		}
	}

	err = json.Unmarshal([]byte(statusJsonRaw), obj)
	if err != nil {
		return err
	}

	// sets the resource version from manifest work resource version
	// The version will be passed back when making an update request
	obj.SetResourceVersion(manifestWork.GetResourceVersion())

	// set creation/deletion timestamps from Maestro's system generated values
	obj.SetDeletionTimestamp(manifestWork.GetDeletionTimestamp())
	obj.SetCreationTimestamp(manifestWork.GetCreationTimestamp())
	return nil
}

// wrapsK8sObjectInMaestroManifestBundle wraps the received object onto a Maestro's manifest bundle
// for delivery onto the spoke cluster identified by this client's namespace
func (c *maestroCSClient) wrapsK8sObjectInMaestroManifestBundle(obj client.Object) (*workv1.ManifestWork, error) {
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
	labels[resourceTypeLabel] = c.gvkToUuid(gvk)

	return &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       c.namespace,
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
func (c *maestroCSClient) generateUniqueMaestroManifestBundleName(obj client.Object) (string, error) {
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
func (c *maestroCSClient) gvkToUuid(gvk schema.GroupVersionKind) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(gvk.String())).String()
}

// unwrapsRawObjectFromManifestBundle unwraps the raw object from the manifest bundle
// The logic applied is as follows
// If the manifest bundle is a readonly, then we retrieve the raw object from the status feedback
// Otherwise, in case of k8s resources created by CS via Maestro, CS own the manifest which
// is the spec and Maestro own the status.
// parsing of the object will happen in two steps;
// 1. Parse the manifest first i.e the spec owned by CS
// 2. Retrieve the status feedback result and parse it and append it
// to the result above to get a complete object
func (c *maestroCSClient) unwrapsRawObjectFromManifestBundle(
	ctx context.Context,
	logger sdklogging.Logger,
	mw *workv1.ManifestWork) (string, bool) {
	if len(mw.Spec.Workload.Manifests) != 1 ||
		len(mw.Spec.ManifestConfigs) != 1 {
		logger.Warn(ctx, "Maestro's manifest bundle with id '%s' is invalid", mw.UID)
		return "", false
	}

	config := mw.Spec.ManifestConfigs[0]

	if config.UpdateStrategy.Type != workv1.UpdateStrategyTypeReadOnly {
		manifest := mw.Spec.Workload.Manifests[0]
		b, err := manifest.MarshalJSON()
		if err != nil {
			logger.Error(ctx, "Could not marshal the manifest wrapped within Maestro's manifest bundle with id '%s'", mw.UID)
			return "", false
		}
		status, statusExists := c.getStatusFeedbackValue(ctx, logger, mw)

		if !statusExists {
			logger.Debug(
				ctx, "The manifest's resource wrapped within Maestro's manifest bundle with id '%s' "+
					"does not have a status feedback value", mw.UID,
			)
			return string(b), true
		}

		// merge manifest content and its status to return complete object
		var obj map[string]interface{}
		// It is safe to ignore unmarshalling error here, if it fails, the next unmarshal will work on a nil map.
		_ = json.Unmarshal(b, &obj)

		err = json.Unmarshal([]byte(fmt.Sprintf(`{"status": %s}`, status)), &obj)
		if err != nil {
			logger.Error(ctx,
				"Could not unmarshal resource status feedback value containing the status section of the "+
					"manifest's resource wrapped within Maestro's manifest bundle with id '%s'", mw.UID,
			)
			return "", false
		}
		// it is safe to ignore marshalling error here
		b, _ = json.Marshal(obj)
		return string(b), true
	}

	return c.getStatusFeedbackValue(ctx, logger, mw)
}

func (c *maestroCSClient) getStatusFeedbackValue(
	ctx context.Context,
	logger sdklogging.Logger,
	mw *workv1.ManifestWork) (string, bool) {
	config := mw.Spec.ManifestConfigs[0]
	resourceStatus := mw.Status.ResourceStatus

	if len(resourceStatus.Manifests) > 1 {
		logger.Error(
			ctx,
			"Unexpected number of manifests: Maestro's manifest bundle with id '%s' "+
				"has more than one manifest: %d", mw.UID, len(resourceStatus.Manifests),
		)
		return "", false
	}

	if len(resourceStatus.Manifests) == 0 {
		logger.Debug(
			ctx,
			"The resource status of the manifest wrapped within Maestro's manifest bundle with "+
				"id '%s' is not set. Maestro server currently not aware of it",
			mw.UID,
		)

		return "", false
	}

	manifestStatus := resourceStatus.Manifests[0]

	if len(manifestStatus.StatusFeedbacks.Values) == 0 {
		logger.Debug(ctx, "Manifest's resource wrapped within Maestro's manifest bundle with id '%s' "+
			"has no manifest status feedback values", mw.UID,
		)
		return "", false
	}

	jsonPathName := statusOnlyJsonPathName
	if config.UpdateStrategy.Type == workv1.UpdateStrategyTypeReadOnly {
		jsonPathName = readOnlyResourceJsonPathName
	}

	for _, val := range manifestStatus.StatusFeedbacks.Values {
		if val.Name == jsonPathName && val.Value.Type == workv1.JsonRaw {
			return *val.Value.JsonRaw, true
		}
	}

	logger.Error(ctx, "Could not find status feedback value with name '%s' of type '%s' "+
		"for the manifest's resource wrapped within Maestro's manifest bundle with id '%s'",
		jsonPathName, workv1.JsonRaw, mw.UID,
	)
	return "", false
}
