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

package verifiers

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"
)

func createArbitraryResource(ctx context.Context, dynamicClient dynamic.Interface, namespace string, resourceBytes []byte) (*unstructured.Unstructured, error) {
	desiredObj := &unstructured.Unstructured{}
	if err := yaml.Unmarshal(resourceBytes, desiredObj); err != nil {
		return nil, err
	}
	desiredObj.SetNamespace(namespace)

	restMapping, err := localRESTMapper.RESTMapping(desiredObj.GroupVersionKind().GroupKind(), desiredObj.GroupVersionKind().Version)
	if err != nil {
		return nil, fmt.Errorf("failed to get RESTMapping for %v: %w", desiredObj.GroupVersionKind(), err)
	}

	if restMapping.Scope.Name() == meta.RESTScopeNameRoot {
		return dynamicClient.Resource(restMapping.Resource).Create(ctx, desiredObj, metav1.CreateOptions{})
	}

	return dynamicClient.Resource(restMapping.Resource).Namespace(namespace).Create(ctx, desiredObj, metav1.CreateOptions{})
}

// defaultRESTMappings contains RESTMappings we use in our e2e tests to avoid wiring a complete RESTMapper to eliminate one possible
// source of error. If this becomes painful, then replace it with a real RESTMapper based on discovery.
var defaultRESTMappings = []meta.RESTMapping{
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ServiceAccount"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "", Version: "v1", Resource: "serviceaccounts"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Service"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "DaemonSet"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "apiextensions.k8s.io", Version: "v1", Kind: "CustomResourceDefinition"},
		Scope:            meta.RESTScopeRoot,
		Resource:         schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole"},
		Scope:            meta.RESTScopeRoot,
		Resource:         schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRoleBinding"},
		Scope:            meta.RESTScopeRoot,
		Resource:         schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "Role"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "RoleBinding"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "route.openshift.io", Version: "v1", Kind: "Route"},
		Scope:            meta.RESTScopeNamespace,
		Resource:         schema.GroupVersionResource{Group: "route.openshift.io", Version: "v1", Resource: "routes"},
	},
	{
		GroupVersionKind: schema.GroupVersionKind{Group: "security.openshift.io", Version: "v1", Kind: "SecurityContextConstraints"},
		Scope:            meta.RESTScopeRoot,
		Resource:         schema.GroupVersionResource{Group: "security.openshift.io", Version: "v1", Resource: "securitycontextconstraints"},
	},
}

var localRESTMapper = newHardcodedRESTMapper()

func newHardcodedRESTMapper() hardCodedFirstRESTMapper {
	ret := hardCodedFirstRESTMapper{
		Mapping: map[schema.GroupVersionKind]meta.RESTMapping{},
	}
	for i := range defaultRESTMappings {
		curr := defaultRESTMappings[i]
		ret.Mapping[curr.GroupVersionKind] = curr
	}
	return ret
}

// hardCodedFirstRESTMapper is a RESTMapper that will look for hardcoded mappings.  This was simple when small. If it
// becomes painful, replace with a real RESTMapper based on discovery.  The disadvantage to discovery are the problems
// we have if CRD registration or APIService registration fails.
type hardCodedFirstRESTMapper struct {
	Mapping map[schema.GroupVersionKind]meta.RESTMapping
}

func (m hardCodedFirstRESTMapper) String() string {
	return fmt.Sprintf("HardCodedRESTMapper{\n\t%v\n}", m.Mapping)
}

func (m hardCodedFirstRESTMapper) RESTMapping(gk schema.GroupKind, version string) (*meta.RESTMapping, error) {
	gvk := gk.WithVersion(version)

	single, ok := m.Mapping[gvk]
	// not handled, fail so we notice
	if !ok {
		return nil, fmt.Errorf("no mapping for %v", gvk)
	}

	return &single, nil
}
