// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package autonode

import (
	"encoding/json"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// autoNodeApplyDesireName identifies the required HostedCluster configuration.
// A second cluster-scoped desire can set or withdraw the HCP operand image.
const autoNodeApplyDesireName = "autonode-karpenter-poc"

const autoNodeOperandImageApplyDesireName = "autonode-karpenter-operand-image"

// autoNodeFieldManager is a dedicated, non-default field manager for this
// controller's server-side-apply writes. Using a distinct manager (rather
// than kube-applier's default) keeps ownership of spec.autoNode clearly
// attributable to this controller, separate from any other writer that may
// still be reconciling the rest of the HostedCluster object (e.g. CS's
// ManifestWork - see the package-level comment on autoNodeSpec for why that
// doesn't conflict), and makes manual field removal unambiguous if ever
// needed (`kubectl patch ... --field-manager=aro-hcp-autonode-validation`
// style removal, not ad hoc edits).
const autoNodeFieldManager = "aro-hcp-autonode-validation"

// partialHostedCluster is the minimal HostedCluster shape this controller
// server-side-applies. It intentionally carries only apiVersion/kind,
// identifying metadata, and spec.autoNode — never a full HostedCluster (or
// even a full HostedClusterSpec), so this field manager only ever claims
// ownership of the one field it actually cares about and never fights any
// other writer over the rest of the object.
type partialHostedCluster struct {
	metav1.TypeMeta `json:",inline"`
	Metadata        partialObjectMeta        `json:"metadata"`
	Spec            partialHostedClusterSpec `json:"spec"`
}

type partialObjectMeta struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// HyperShift mirrors the operator image from HostedCluster to HostedControlPlane.
// The standalone operator reads the operand image directly from HostedControlPlane.
// The old hypershift.openshift.io/karpenter-provider-azure-image HC key has no reader.
const (
	karpenterOperatorImageAnnotation = "hypershift.openshift.io/karpenter-operator-image"
	karpenterOperandImageAnnotation  = "autoscaling.openshift.io/karpenter-image"
)

// ImageOverrides carries personal-dev-only image references for the
// standalone karpenter-operator and its karpenter-provider-azure operand.
// Both fields must stay empty outside personal dev. The config field
// karpenterProviderAzureImageOverride sets the supported HCP operand annotation
// through a separate desire. A cleared override withdraws our previous annotation.
type ImageOverrides struct {
	KarpenterOperatorImage      string
	KarpenterProviderAzureImage string
}

func (o ImageOverrides) hostedClusterAnnotations() map[string]string {
	annotations := map[string]string{}
	if o.KarpenterOperatorImage != "" {
		annotations[karpenterOperatorImageAnnotation] = o.KarpenterOperatorImage
	}
	if len(annotations) == 0 {
		return nil
	}
	return annotations
}

type partialHostedClusterSpec struct {
	AutoNode autoNodeSpec `json:"autoNode"`
}

// autoNodeSpec, provisionerConfigSpec, karpenterConfigSpec, and
// karpenterAzureConfigSpec are local mirrors of the equivalent types in the
// custom hypershift-operator fork that adds Azure support
// (api/hypershift/v1beta1/hostedcluster_types.go: AutoNode, ProvisionerConfig,
// KarpenterConfig, KarpenterAzureConfig). They are defined locally, rather
// than imported from hsv1beta1, because no published github.com/openshift/hypershift/api
// version has the Azure member yet — stock upstream's KarpenterConfig only
// supports AWS. The only fork that has it requires go 1.26.0 / k8s.io/api
// v0.36.2, versus this workspace's go 1.25.7 / k8s.io/api v0.35.3 across the
// full go.work; a replace directive would drag the entire 40-module workspace
// to a new toolchain for one field, and would still point at an unpublished
// personal branch. Hand-marshaling this small a shape is materially cheaper
// than that, and it's safe: CS (which builds the full typed HostedCluster for
// its own ManifestWork) never contends for this field, because its pinned
// AutoNode struct field uses `json:"autoNode,omitzero"` — a zero-valued
// AutoNode is correctly omitted from the wire payload — and CS's ManifestWork
// uses server-side-apply, so an omitted field doesn't clobber this
// controller's write. Both of those facts must be re-verified on any future
// hypershift/api version bump in either this repo or CS: a switch to
// `omitempty` (which never omits a non-pointer struct) or CS moving off SSA
// would silently make CS a competing owner of spec.autoNode.
// Revisit hand-marshaling once the Azure field merges upstream on a version
// this workspace can pin without a toolchain bump — at that point this
// becomes a one-file change back to importing hsv1beta1 directly. Field
// names/json tags here must be kept in sync with the fork's schema until then.
type autoNodeSpec struct {
	Provisioner provisionerConfigSpec `json:"provisionerConfig"`
}

type provisionerConfigSpec struct {
	Name      string              `json:"name"`
	Karpenter karpenterConfigSpec `json:"karpenter"`
}

type karpenterConfigSpec struct {
	Platform string                   `json:"platform"`
	Azure    karpenterAzureConfigSpec `json:"azure"`
}

type karpenterAzureConfigSpec struct {
	ClientID string `json:"clientID"`
}

// buildAutoNodePayload builds the partial-HostedCluster JSON that turns on
// Azure Karpenter AutoNode. clientID must be non-empty: the fork's
// KarpenterAzureConfig.ClientID is a required field once
// KarpenterConfig.Platform is Azure (the karpenter-operator hard-errors
// without it), so callers must gate on a non-empty clientID before calling
// this — see autoNodeSyncer.SyncOnce.
func buildAutoNodePayload(target kubeapplierapi.ResourceReference, clientID string, imageOverrides ImageOverrides) ([]byte, error) {
	if clientID == "" {
		return nil, utils.TrackError(fmt.Errorf("clientID must not be empty"))
	}
	obj := partialHostedCluster{
		TypeMeta: metav1.TypeMeta{
			APIVersion: hsv1beta1.SchemeGroupVersion.String(),
			Kind:       "HostedCluster",
		},
		Metadata: partialObjectMeta{
			Name:        target.Name,
			Namespace:   target.Namespace,
			Annotations: imageOverrides.hostedClusterAnnotations(),
		},
		Spec: partialHostedClusterSpec{
			AutoNode: autoNodeSpec{
				Provisioner: provisionerConfigSpec{
					// "Karpenter": matches hsv1beta1.ProvisionerKarpenter's
					// value in both stock upstream and the fork.
					Name: "Karpenter",
					Karpenter: karpenterConfigSpec{
						Platform: "Azure",
						Azure: karpenterAzureConfigSpec{
							ClientID: clientID,
						},
					},
				},
			},
		},
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to marshal partial HostedCluster AutoNode payload: %w", err))
	}
	return raw, nil
}

// buildAutoNodeApplyDesire builds the cluster-scoped ApplyDesire that
// server-side-applies spec.autoNode onto the cluster's existing HostedCluster.
func buildAutoNodeApplyDesire(
	subscriptionID, resourceGroupName, clusterName string,
	managementClusterResourceID *azcorearm.ResourceID,
	target kubeapplierapi.ResourceReference,
	clientID string,
	imageOverrides ImageOverrides,
) (*kubeapplierapi.ApplyDesire, error) {
	resourceIDStr := kubeapplierapihelpers.ToClusterScopedApplyDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, autoNodeApplyDesireName,
	)
	resourceID, err := azcorearm.ParseResourceID(resourceIDStr)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse ApplyDesire resource ID: %w", err))
	}

	raw, err := buildAutoNodePayload(target, clientID, imageOverrides)
	if err != nil {
		return nil, err
	}

	return &kubeapplierapi.ApplyDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(managementClusterResourceID.String()),
		},
		Spec: kubeapplierapi.ApplyDesireSpec{
			ManagementCluster: managementClusterResourceID,
			Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
			TargetItem:        target,
			ServerSideApply: &kubeapplierapi.ServerSideApplyConfig{
				FieldManager: ptr.To(autoNodeFieldManager),
				KubeContent:  &runtime.RawExtension{Raw: raw},
			},
		},
		Tags: map[string]string{
			kubeapplierapi.TagControllerName: AutoNodeEnablerControllerName,
		},
	}, nil
}

// buildAutoNodeOperandImageApplyDesire manages only the HCP operand annotation.
// An empty image produces an explicit empty map to withdraw our owned key.
// An absent HCP can reject this partial manifest. Kube-applier retries on resync.
func buildAutoNodeOperandImageApplyDesire(
	subscriptionID, resourceGroupName, clusterName string,
	managementClusterResourceID *azcorearm.ResourceID,
	target kubeapplierapi.ResourceReference,
	image string,
) (*kubeapplierapi.ApplyDesire, error) {
	if target.Namespace == "" || target.Name == "" || target.Group != hsv1beta1.SchemeGroupVersion.Group ||
		target.Version != hsv1beta1.SchemeGroupVersion.Version || target.Resource != "hostedcontrolplanes" {
		return nil, utils.TrackError(fmt.Errorf("invalid HostedControlPlane target: %+v", target))
	}
	annotations := map[string]string{}
	if image != "" {
		annotations[karpenterOperandImageAnnotation] = image
	}
	obj := map[string]any{
		"apiVersion": hsv1beta1.SchemeGroupVersion.String(),
		"kind":       "HostedControlPlane",
		"metadata": map[string]any{
			"name": target.Name, "namespace": target.Namespace, "annotations": annotations,
		},
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("marshal HCP operand image payload: %w", err))
	}
	resourceID, err := azcorearm.ParseResourceID(kubeapplierapihelpers.ToClusterScopedApplyDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, autoNodeOperandImageApplyDesireName,
	))
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("parse HCP operand image desire ID: %w", err))
	}
	return &kubeapplierapi.ApplyDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID: resourceID, PartitionKey: strings.ToLower(managementClusterResourceID.String()),
		},
		Spec: kubeapplierapi.ApplyDesireSpec{
			ManagementCluster: managementClusterResourceID,
			Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
			TargetItem:        target,
			ServerSideApply: &kubeapplierapi.ServerSideApplyConfig{
				FieldManager: ptr.To(autoNodeFieldManager), KubeContent: &runtime.RawExtension{Raw: raw},
			},
		},
		Tags: map[string]string{kubeapplierapi.TagControllerName: AutoNodeEnablerControllerName},
	}, nil
}
