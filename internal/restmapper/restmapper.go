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

package restmapper

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/openshift/library-go/pkg/client/openshiftrestmapper"
)

// additionalRESTMappings contains mappings for standard Kubernetes resources
// not already covered by library-go's hardcoded OpenShift mapper.
var additionalRESTMappings = []meta.RESTMapping{
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Namespace"},
		Scope:            meta.RESTScopeRoot,
		Resource:         schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Service"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "PersistentVolumeClaim"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "", Version: "v1", Resource: "persistentvolumeclaims"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "PersistentVolume"},
		Scope:            meta.RESTScopeRoot,
		Resource:         schema.GroupVersionResource{Group: "", Version: "v1", Resource: "persistentvolumes"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Endpoints"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "", Version: "v1", Resource: "endpoints"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ResourceQuota"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "", Version: "v1", Resource: "resourcequotas"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "LimitRange"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "", Version: "v1", Resource: "limitranges"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "policy", Version: "v1", Kind: "PodDisruptionBudget"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "policy", Version: "v1", Resource: "poddisruptionbudgets"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "CronJob"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "autoscaling", Version: "v2", Kind: "HorizontalPodAutoscaler"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "hypershift.openshift.io", Version: "v1beta1", Kind: "HostedCluster"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "hypershift.openshift.io", Version: "v1beta1", Resource: "hostedclusters"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "hypershift.openshift.io", Version: "v1beta1", Kind: "NodePool"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "hypershift.openshift.io", Version: "v1beta1", Resource: "nodepools"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "multitenancy.acn.azure.com", Version: "v1alpha1", Kind: "PodNetwork"},
		Scope:            meta.RESTScopeRoot,
		Resource:         schema.GroupVersionResource{Group: "multitenancy.acn.azure.com", Version: "v1alpha1", Resource: "podnetworks"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "multitenancy.acn.azure.com", Version: "v1alpha1", Kind: "PodNetworkInstance"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "multitenancy.acn.azure.com", Version: "v1alpha1", Resource: "podnetworkinstances"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "secrets-store.csi.x-k8s.io", Version: "v1", Kind: "SecretProviderClass"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "secrets-store.csi.x-k8s.io", Version: "v1", Resource: "secretproviderclasses"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "secret-sync.x-k8s.io", Version: "v1alpha1", Kind: "SecretSync"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "secret-sync.x-k8s.io", Version: "v1alpha1", Resource: "secretsyncs"},
	},
}

// Mapper is a package-level RESTMapper that resolves GVK→GVR without API
// server discovery. It chains:
//  1. Additional standard Kubernetes mappings not in library-go
//  2. Library-go's hardcoded OpenShift/Kubernetes mappings
//  3. An always-error fallback
var Mapper meta.RESTMapper = newHardCodedRESTMapper()

func newHardCodedRESTMapper() meta.RESTMapper {
	libraryGoRESTMapping := openshiftrestmapper.NewOpenShiftHardcodedRESTMapper(alwaysErrorRESTMapper{})

	ret := openshiftrestmapper.HardCodedFirstRESTMapper{
		Mapping:    map[schema.GroupVersionKind]meta.RESTMapping{},
		RESTMapper: libraryGoRESTMapping,
	}
	for i := range additionalRESTMappings {
		curr := additionalRESTMappings[i]
		ret.Mapping[curr.GroupVersionKind] = curr
	}
	return ret
}

// ResourceFor resolves a GVK to a GVR using the hardcoded mapper chain.
func ResourceFor(gvk schema.GroupVersionKind) (schema.GroupVersionResource, error) {
	mapping, err := Mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return schema.GroupVersionResource{}, err
	}
	return mapping.Resource, nil
}

// alwaysErrorRESTMapper is a terminal delegate that rejects all lookups.
type alwaysErrorRESTMapper struct{}

var _ meta.RESTMapper = alwaysErrorRESTMapper{}

func (alwaysErrorRESTMapper) KindFor(schema.GroupVersionResource) (schema.GroupVersionKind, error) {
	return schema.GroupVersionKind{}, &meta.NoResourceMatchError{}
}

func (alwaysErrorRESTMapper) KindsFor(schema.GroupVersionResource) ([]schema.GroupVersionKind, error) {
	return nil, &meta.NoResourceMatchError{}
}

func (alwaysErrorRESTMapper) ResourceFor(schema.GroupVersionResource) (schema.GroupVersionResource, error) {
	return schema.GroupVersionResource{}, &meta.NoResourceMatchError{}
}

func (alwaysErrorRESTMapper) ResourcesFor(schema.GroupVersionResource) ([]schema.GroupVersionResource, error) {
	return nil, &meta.NoResourceMatchError{}
}

func (alwaysErrorRESTMapper) RESTMapping(gk schema.GroupKind, _ ...string) (*meta.RESTMapping, error) {
	return nil, fmt.Errorf("no hardcoded REST mapping for %v", gk)
}

func (alwaysErrorRESTMapper) RESTMappings(gk schema.GroupKind, _ ...string) ([]*meta.RESTMapping, error) {
	return nil, fmt.Errorf("no hardcoded REST mapping for %v", gk)
}

func (alwaysErrorRESTMapper) ResourceSingularizer(string) (string, error) {
	return "", fmt.Errorf("no hardcoded singularizer")
}
