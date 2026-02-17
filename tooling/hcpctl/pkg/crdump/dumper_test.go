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

package crdump

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDumper_DumpCRs(t *testing.T) {
	scheme := newTestScheme()

	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-hc-ns",
			Labels: map[string]string{
				clusterIDLabelKey: "cluster-123",
			},
		},
	}

	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "hostedclusters.hypershift.openshift.io"},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "hypershift.openshift.io",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:     "HostedCluster",
				ListKind: "HostedClusterList",
				Plural:   "hostedclusters",
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{Name: "v1beta1", Storage: true, Served: true},
			},
		},
	}

	gvk := schema.GroupVersionKind{
		Group:   crd.Spec.Group,
		Version: crd.Spec.Versions[0].Name,
		Kind:    crd.Spec.Names.Kind,
	}
	listGVK := schema.GroupVersionKind{
		Group:   crd.Spec.Group,
		Version: crd.Spec.Versions[0].Name,
		Kind:    crd.Spec.Names.ListKind,
	}
	scheme.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(listGVK, &unstructured.UnstructuredList{})

	cr := createUnstructuredCR("hypershift.openshift.io/v1beta1", "HostedCluster", "my-hc", "test-hc-ns", nil)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(namespace, crd, cr).Build()
	lister := NewCustomResourceLister(fakeClient)

	var collectedCRs []*unstructured.UnstructuredList
	customOutputFunc := func(crChan <-chan *unstructured.UnstructuredList, options CROutputOptions) error {
		for crList := range crChan {
			collectedCRs = append(collectedCRs, crList)
		}
		return nil
	}

	dumper := NewDumper(lister, customOutputFunc, CROutputOptions{})

	err := dumper.DumpCRs(context.Background(), "test-hc-ns")

	require.NoError(t, err)
	assert.Len(t, collectedCRs, 1)
	assert.Len(t, collectedCRs[0].Items, 1)
	assert.Equal(t, "my-hc", collectedCRs[0].Items[0].GetName())
}
