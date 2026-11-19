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
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	HostedClustersGVR = schema.GroupVersionResource{
		Group: "hypershift.openshift.io", Version: "v1beta1", Resource: "hostedclusters",
	}
	NodePoolsGVR = schema.GroupVersionResource{
		Group: "hypershift.openshift.io", Version: "v1beta1", Resource: "nodepools",
	}
	PodNetworksGVR = schema.GroupVersionResource{
		Group: "multitenancy.acn.azure.com", Version: "v1alpha1", Resource: "podnetworks",
	}
	PodNetworkInstancesGVR = schema.GroupVersionResource{
		Group: "multitenancy.acn.azure.com", Version: "v1alpha1", Resource: "podnetworkinstances",
	}
	SecretProviderClassesGVR = schema.GroupVersionResource{
		Group: "secrets-store.csi.x-k8s.io", Version: "v1", Resource: "secretproviderclasses",
	}
	SecretSyncsGVR = schema.GroupVersionResource{
		Group: "secret-sync.x-k8s.io", Version: "v1alpha1", Resource: "secretsyncs",
	}
	ConfigMapsGVR = schema.GroupVersionResource{
		Group: "", Version: "v1", Resource: "configmaps",
	}
	SecretsGVR = schema.GroupVersionResource{
		Group: "", Version: "v1", Resource: "secrets",
	}
)

// ensurePodNetworksNotACMOwned lists cluster-scoped PodNetworks and blocks
// while any are still owned by ACM.
func (c *Controller) ensurePodNetworksNotACMOwned(ctx context.Context) (stepResult, error) {
	return c.ensureResourcesNotACMOwned(ctx, "PodNetwork", func(ctx context.Context) ([]unstructured.Unstructured, error) {
		return listClusterScoped(ctx, c.dynamicClient, PodNetworksGVR)
	})
}

// ensurePodNetworkInstancesNotACMOwned lists PodNetworkInstances and blocks
// while any are still owned by ACM.
func (c *Controller) ensurePodNetworkInstancesNotACMOwned(ctx context.Context) (stepResult, error) {
	return c.ensureResourcesNotACMOwned(ctx, "PodNetworkInstance", func(ctx context.Context) ([]unstructured.Unstructured, error) {
		return listNamespaced(ctx, c.dynamicClient, PodNetworkInstancesGVR, metav1.NamespaceAll)
	})
}

func (c *Controller) ensureResourcesNotACMOwned(
	ctx context.Context,
	kind string,
	list func(context.Context) ([]unstructured.Unstructured, error),
) (stepResult, error) {
	items, err := list(ctx)
	if err != nil {
		return stepResult{}, err
	}
	owned := make([]string, 0)
	for i := range items {
		if ownedByACM(&items[i]) {
			owned = append(owned, objectRef(&items[i]))
		}
	}
	if len(owned) == 0 {
		return stepResult{done: true}, nil
	}
	return stepResult{
		done:   false,
		reason: kind + " resources still owned by ACM",
		values: []any{"remaining", len(owned), "resources", owned},
	}, nil
}

// ensureHostedClusterResourcesNotACMOwned verifies HostedClusters and the
// resources in each HostedCluster namespace, plus each derived HostedControlPlane
// namespace (<hc-namespace>-<hc-name>), are no longer ACM-owned. This is a
// prerequisite for deleting MCE.
func (c *Controller) ensureHostedClusterResourcesNotACMOwned(ctx context.Context) (stepResult, error) {
	return c.ensureResourcesNotACMOwned(ctx, "HostedCluster", func(ctx context.Context) ([]unstructured.Unstructured, error) {
		tracked, err := listHostedClusterOwnershipResources(ctx, c.dynamicClient)
		if err != nil {
			return nil, err
		}
		items := make([]unstructured.Unstructured, 0, len(tracked))
		for i := range tracked {
			items = append(items, tracked[i].obj)
		}
		return items, nil
	})
}

// ownedByACM reports whether any ownerReference is from ACM
// (AppliedManifestWork or any open-cluster-management.io API group).
func ownedByACM(obj metav1.Object) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if isACMOwnerReference(ref) {
			return true
		}
	}
	return false
}

func stringsHasOCMSuffix(group string) bool {
	return strings.HasSuffix(group, "open-cluster-management.io")
}

func objectRef(obj *unstructured.Unstructured) string {
	if len(obj.GetNamespace()) == 0 {
		return fmt.Sprintf("%s/%s", obj.GetKind(), obj.GetName())
	}
	return fmt.Sprintf("%s/%s/%s", obj.GetKind(), obj.GetNamespace(), obj.GetName())
}
