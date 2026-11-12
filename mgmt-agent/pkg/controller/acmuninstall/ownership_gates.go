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
		return c.listClusterScoped(ctx, PodNetworksGVR)
	})
}

// ensurePodNetworkInstancesNotACMOwned lists PodNetworkInstances and blocks
// while any are still owned by ACM.
func (c *Controller) ensurePodNetworkInstancesNotACMOwned(ctx context.Context) (stepResult, error) {
	return c.ensureResourcesNotACMOwned(ctx, "PodNetworkInstance", func(ctx context.Context) ([]unstructured.Unstructured, error) {
		return c.listNamespaced(ctx, PodNetworkInstancesGVR, metav1.NamespaceAll)
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
	return c.ensureResourcesNotACMOwned(ctx, "HostedCluster", c.listHostedClusterOwnershipResources)
}

func (c *Controller) listHostedClusterOwnershipResources(ctx context.Context) ([]unstructured.Unstructured, error) {
	namespacesByName, err := c.namespacesByName(ctx)
	if err != nil {
		return nil, err
	}

	hostedClusters, err := c.listNamespaced(ctx, HostedClustersGVR, metav1.NamespaceAll)
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
	resources := make([]unstructured.Unstructured, 0, len(hostedClusters))
	for i := range hostedClusters {
		hc := &hostedClusters[i]
		resources = append(resources, *hc)

		if ns, ok := namespacesByName[hc.GetNamespace()]; ok {
			resources = append(resources, *ns)
		}

		nsResources, err := c.listInNamespace(ctx, hc.GetNamespace(), hcNamespaceGVRs)
		if err != nil {
			return nil, fmt.Errorf("list HostedCluster namespace %q: %w", hc.GetNamespace(), err)
		}
		resources = append(resources, nsResources...)

		// HostedControlPlane namespace is always <hc-namespace>-<hc-name>.
		hcpNSName := hc.GetNamespace() + "-" + hc.GetName()
		if hcpNS, ok := namespacesByName[hcpNSName]; ok {
			resources = append(resources, *hcpNS)
		}
	}
	return resources, nil
}

func (c *Controller) listInNamespace(ctx context.Context, namespace string, gvrs []schema.GroupVersionResource) ([]unstructured.Unstructured, error) {
	resources := make([]unstructured.Unstructured, 0)
	for _, gvr := range gvrs {
		items, err := c.listNamespaced(ctx, gvr, namespace)
		if err != nil {
			return nil, err
		}
		resources = append(resources, items...)
	}
	return resources, nil
}

func (c *Controller) namespacesByName(ctx context.Context) (map[string]*unstructured.Unstructured, error) {
	items, err := c.listClusterScoped(ctx, schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"})
	if err != nil {
		return nil, err
	}
	byName := make(map[string]*unstructured.Unstructured, len(items))
	for i := range items {
		byName[items[i].GetName()] = &items[i]
	}
	return byName, nil
}

func (c *Controller) listClusterScoped(ctx context.Context, gvr schema.GroupVersionResource) ([]unstructured.Unstructured, error) {
	list, err := c.dynamicClient.Resource(gvr).List(ctx, metav1.ListOptions{})
	if apiGone(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", gvr.String(), err)
	}
	return list.Items, nil
}

func (c *Controller) listNamespaced(ctx context.Context, gvr schema.GroupVersionResource, namespace string) ([]unstructured.Unstructured, error) {
	list, err := c.dynamicClient.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if apiGone(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list %s in namespace %q: %w", gvr.String(), namespace, err)
	}
	return list.Items, nil
}

// ownedByACM reports whether any ownerReference is from ACM
// (AppliedManifestWork or any open-cluster-management.io API group).
func ownedByACM(obj metav1.Object) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.Kind == "AppliedManifestWork" {
			return true
		}
		gv, err := schema.ParseGroupVersion(ref.APIVersion)
		if err != nil {
			continue
		}
		if strings.HasSuffix(gv.Group, "open-cluster-management.io") {
			return true
		}
	}
	return false
}

func objectRef(obj *unstructured.Unstructured) string {
	if obj.GetNamespace() == "" {
		return fmt.Sprintf("%s/%s", obj.GetKind(), obj.GetName())
	}
	return fmt.Sprintf("%s/%s/%s", obj.GetKind(), obj.GetNamespace(), obj.GetName())
}
