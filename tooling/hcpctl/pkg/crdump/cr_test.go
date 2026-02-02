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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = apiextensionsv1.AddToScheme(scheme)
	return scheme
}

func newFakeClient(scheme *runtime.Scheme, objs ...client.Object) client.Client {
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func createUnstructuredCR(apiVersion, kind, name, namespace string, labels map[string]string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": apiVersion,
			"kind":       kind,
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
		},
	}
	if labels != nil {
		obj.SetLabels(labels)
	}
	return obj
}

func createClusterScopedUnstructuredCR(apiVersion, kind, name string, labels map[string]string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": apiVersion,
			"kind":       kind,
			"metadata": map[string]interface{}{
				"name": name,
			},
		},
	}
	if labels != nil {
		obj.SetLabels(labels)
	}
	return obj
}

func TestListCRDs(t *testing.T) {
	testCases := []struct {
		name          string
		crds          []client.Object
		expectedCount int
		expectedNames []string
	}{
		{
			name: "returns all CRDs",
			crds: []client.Object{
				&apiextensionsv1.CustomResourceDefinition{
					ObjectMeta: metav1.ObjectMeta{Name: "hostedclusters.hypershift.openshift.io"},
				},
				&apiextensionsv1.CustomResourceDefinition{
					ObjectMeta: metav1.ObjectMeta{Name: "nodepools.hypershift.openshift.io"},
				},
			},
			expectedCount: 2,
			expectedNames: []string{"hostedclusters.hypershift.openshift.io", "nodepools.hypershift.openshift.io"},
		},
		{
			name:          "returns empty list when no CRDs exist",
			crds:          []client.Object{},
			expectedCount: 0,
			expectedNames: []string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			scheme := newTestScheme()
			fakeClient := newFakeClient(scheme, tc.crds...)
			lister := NewCustomResourceLister(fakeClient)
			ctx := context.Background()

			crdList, err := lister.ListCRDs(ctx)

			require.NoError(t, err)
			require.NotNil(t, crdList)
			assert.Len(t, crdList.Items, tc.expectedCount)

			actualNames := make([]string, len(crdList.Items))
			for i, crd := range crdList.Items {
				actualNames[i] = crd.Name
			}
			assert.ElementsMatch(t, tc.expectedNames, actualNames)
		})
	}
}

func TestListCRs(t *testing.T) {
	testCases := []struct {
		name                   string
		namespace              *corev1.Namespace
		crds                   []*apiextensionsv1.CustomResourceDefinition
		crs                    []*unstructured.Unstructured
		hostedClusterNamespace string
		expectedCRListCount    int
		expectedTotalCRs       int
		expectedError          string
	}{
		{
			name: "returns CRs for namespace-scoped CRD",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hc-ns",
					Labels: map[string]string{
						clusterIDLabelKey: "cluster-123",
					},
				},
			},
			crds: []*apiextensionsv1.CustomResourceDefinition{
				{
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
				},
			},
			crs: []*unstructured.Unstructured{
				createUnstructuredCR("hypershift.openshift.io/v1beta1", "HostedCluster", "my-hc", "test-hc-ns", nil),
				createUnstructuredCR("hypershift.openshift.io/v1beta1", "HostedCluster", "another-hc", "test-hc-ns", nil),
			},
			hostedClusterNamespace: "test-hc-ns",
			expectedCRListCount:    1,
			expectedTotalCRs:       2,
		},
		{
			name: "filters CRs by namespace - only returns CRs in target namespace",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hc-ns",
					Labels: map[string]string{
						clusterIDLabelKey: "cluster-123",
					},
				},
			},
			crds: []*apiextensionsv1.CustomResourceDefinition{
				{
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
				},
			},
			crs: []*unstructured.Unstructured{
				createUnstructuredCR("hypershift.openshift.io/v1beta1", "HostedCluster", "my-hc", "test-hc-ns", nil),
				createUnstructuredCR("hypershift.openshift.io/v1beta1", "HostedCluster", "other-hc", "other-ns", nil),
			},
			hostedClusterNamespace: "test-hc-ns",
			expectedCRListCount:    1,
			expectedTotalCRs:       1,
		},
		{
			name: "skips cluster-scoped non-OCM CRDs",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hc-ns",
					Labels: map[string]string{
						clusterIDLabelKey: "cluster-123",
					},
				},
			},
			crds: []*apiextensionsv1.CustomResourceDefinition{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "clusterwide.example.com"},
					Spec: apiextensionsv1.CustomResourceDefinitionSpec{
						Group: "example.com",
						Names: apiextensionsv1.CustomResourceDefinitionNames{
							Kind:     "ClusterWide",
							ListKind: "ClusterWideList",
							Plural:   "clusterwides",
						},
						Scope: apiextensionsv1.ClusterScoped,
						Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
							{Name: "v1", Storage: true, Served: true},
						},
					},
				},
			},
			crs: []*unstructured.Unstructured{
				createClusterScopedUnstructuredCR("example.com/v1", "ClusterWide", "my-clusterwide", nil),
			},
			hostedClusterNamespace: "test-hc-ns",
			expectedCRListCount:    0,
			expectedTotalCRs:       0,
		},
		{
			name: "returns error when namespace missing cluster ID label",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-hc-ns",
					Labels: map[string]string{},
				},
			},
			crds:                   []*apiextensionsv1.CustomResourceDefinition{},
			crs:                    []*unstructured.Unstructured{},
			hostedClusterNamespace: "test-hc-ns",
			expectedError:          "namespace test-hc-ns missing label api.openshift.com/id",
		},
		{
			name:                   "returns error when namespace does not exist",
			namespace:              nil,
			crds:                   []*apiextensionsv1.CustomResourceDefinition{},
			crs:                    []*unstructured.Unstructured{},
			hostedClusterNamespace: "nonexistent-ns",
			expectedError:          "failed to get namespace 'nonexistent-ns'",
		},
		{
			name: "returns empty list when no CRDs exist",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hc-ns",
					Labels: map[string]string{
						clusterIDLabelKey: "cluster-123",
					},
				},
			},
			crds:                   []*apiextensionsv1.CustomResourceDefinition{},
			crs:                    []*unstructured.Unstructured{},
			hostedClusterNamespace: "test-hc-ns",
			expectedCRListCount:    0,
			expectedTotalCRs:       0,
		},
		{
			name: "returns empty list when CRDs exist but no CRs",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hc-ns",
					Labels: map[string]string{
						clusterIDLabelKey: "cluster-123",
					},
				},
			},
			crds: []*apiextensionsv1.CustomResourceDefinition{
				{
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
				},
			},
			crs:                    []*unstructured.Unstructured{},
			hostedClusterNamespace: "test-hc-ns",
			expectedCRListCount:    0,
			expectedTotalCRs:       0,
		},
		{
			name: "handles multiple namespace-scoped CRDs with CRs",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hc-ns",
					Labels: map[string]string{
						clusterIDLabelKey: "cluster-123",
					},
				},
			},
			crds: []*apiextensionsv1.CustomResourceDefinition{
				{
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
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "nodepools.hypershift.openshift.io"},
					Spec: apiextensionsv1.CustomResourceDefinitionSpec{
						Group: "hypershift.openshift.io",
						Names: apiextensionsv1.CustomResourceDefinitionNames{
							Kind:     "NodePool",
							ListKind: "NodePoolList",
							Plural:   "nodepools",
						},
						Scope: apiextensionsv1.NamespaceScoped,
						Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
							{Name: "v1beta1", Storage: true, Served: true},
						},
					},
				},
			},
			crs: []*unstructured.Unstructured{
				createUnstructuredCR("hypershift.openshift.io/v1beta1", "HostedCluster", "my-hc", "test-hc-ns", nil),
				createUnstructuredCR("hypershift.openshift.io/v1beta1", "NodePool", "my-nodepool-1", "test-hc-ns", nil),
				createUnstructuredCR("hypershift.openshift.io/v1beta1", "NodePool", "my-nodepool-2", "test-hc-ns", nil),
			},
			hostedClusterNamespace: "test-hc-ns",
			expectedCRListCount:    2,
			expectedTotalCRs:       3,
		},
		{
			name: "fetches ManifestWork CRs from local-cluster namespace with cluster ID label",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hc-ns",
					Labels: map[string]string{
						clusterIDLabelKey: "cluster-123",
					},
				},
			},
			crds: []*apiextensionsv1.CustomResourceDefinition{
				{
					ObjectMeta: metav1.ObjectMeta{Name: ManifestWorkCRD},
					Spec: apiextensionsv1.CustomResourceDefinitionSpec{
						Group: "work.open-cluster-management.io",
						Names: apiextensionsv1.CustomResourceDefinitionNames{
							Kind:     "ManifestWork",
							ListKind: "ManifestWorkList",
							Plural:   "manifestworks",
						},
						Scope: apiextensionsv1.NamespaceScoped,
						Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
							{Name: "v1", Storage: true, Served: true},
						},
					},
				},
			},
			crs: []*unstructured.Unstructured{
				createUnstructuredCR("work.open-cluster-management.io/v1", "ManifestWork", "mw-1", "local-cluster", map[string]string{clusterIDLabelKey: "cluster-123"}),
				createUnstructuredCR("work.open-cluster-management.io/v1", "ManifestWork", "mw-2", "local-cluster", map[string]string{clusterIDLabelKey: "cluster-123"}),
				createUnstructuredCR("work.open-cluster-management.io/v1", "ManifestWork", "mw-other", "local-cluster", map[string]string{clusterIDLabelKey: "other-cluster"}),
			},
			hostedClusterNamespace: "test-hc-ns",
			expectedCRListCount:    1,
			expectedTotalCRs:       2,
		},
		{
			name: "fetches ManagedCluster CR matching cluster ID",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hc-ns",
					Labels: map[string]string{
						clusterIDLabelKey: "cluster-123",
					},
				},
			},
			crds: []*apiextensionsv1.CustomResourceDefinition{
				{
					ObjectMeta: metav1.ObjectMeta{Name: ManagedClusterCRD},
					Spec: apiextensionsv1.CustomResourceDefinitionSpec{
						Group: "cluster.open-cluster-management.io",
						Names: apiextensionsv1.CustomResourceDefinitionNames{
							Kind:     "ManagedCluster",
							ListKind: "ManagedClusterList",
							Plural:   "managedclusters",
						},
						Scope: apiextensionsv1.ClusterScoped,
						Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
							{Name: "v1", Storage: true, Served: true},
						},
					},
				},
			},
			crs: []*unstructured.Unstructured{
				createClusterScopedUnstructuredCR("cluster.open-cluster-management.io/v1", "ManagedCluster", "cluster-123", nil),
				createClusterScopedUnstructuredCR("cluster.open-cluster-management.io/v1", "ManagedCluster", "other-cluster", nil),
			},
			hostedClusterNamespace: "test-hc-ns",
			expectedCRListCount:    1,
			expectedTotalCRs:       1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			scheme := newTestScheme()

			for _, crd := range tc.crds {
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
			}

			var objs []client.Object
			if tc.namespace != nil {
				objs = append(objs, tc.namespace)
			}
			for _, crd := range tc.crds {
				objs = append(objs, crd)
			}
			for _, cr := range tc.crs {
				objs = append(objs, cr)
			}

			clientBuilder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...)

			// Add field index for ManagedCluster CRD which uses metadata.name field selector
			for _, crd := range tc.crds {
				if crd.Name == ManagedClusterCRD {
					gvk := schema.GroupVersionKind{
						Group:   crd.Spec.Group,
						Version: crd.Spec.Versions[0].Name,
						Kind:    crd.Spec.Names.Kind,
					}
					obj := &unstructured.Unstructured{}
					obj.SetGroupVersionKind(gvk)
					clientBuilder = clientBuilder.WithIndex(obj, "metadata.name", func(o client.Object) []string {
						return []string{o.GetName()}
					})
				}
			}

			fakeClient := clientBuilder.Build()
			lister := NewCustomResourceLister(fakeClient)
			ctx := context.Background()

			crLists, err := lister.ListCRs(ctx, tc.hostedClusterNamespace)

			if tc.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
				assert.Nil(t, crLists)
			} else {
				require.NoError(t, err)
				assert.Len(t, crLists, tc.expectedCRListCount)

				totalCRs := 0
				for _, crList := range crLists {
					totalCRs += len(crList.Items)
				}
				assert.Equal(t, tc.expectedTotalCRs, totalCRs)
			}
		})
	}
}

func TestListCRs_CRDWithoutStorageVersion(t *testing.T) {
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
		ObjectMeta: metav1.ObjectMeta{Name: "broken.example.com"},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:     "Broken",
				ListKind: "BrokenList",
				Plural:   "brokens",
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{Name: "v1", Storage: false, Served: true},
			},
		},
	}

	fakeClient := newFakeClient(scheme, namespace, crd)
	lister := NewCustomResourceLister(fakeClient)
	ctx := context.Background()

	crLists, err := lister.ListCRs(ctx, "test-hc-ns")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no storage version found for CRD broken.example.com")
	assert.Nil(t, crLists)
}

func TestStreamCRs(t *testing.T) {
	testCases := []struct {
		name                   string
		namespace              *corev1.Namespace
		crds                   []*apiextensionsv1.CustomResourceDefinition
		crs                    []*unstructured.Unstructured
		hostedClusterNamespace string
		expectedCRListCount    int
		expectedTotalCRs       int
		expectedError          string
	}{
		{
			name: "streams CRs through channel",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hc-ns",
					Labels: map[string]string{
						clusterIDLabelKey: "cluster-123",
					},
				},
			},
			crds: []*apiextensionsv1.CustomResourceDefinition{
				{
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
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "nodepools.hypershift.openshift.io"},
					Spec: apiextensionsv1.CustomResourceDefinitionSpec{
						Group: "hypershift.openshift.io",
						Names: apiextensionsv1.CustomResourceDefinitionNames{
							Kind:     "NodePool",
							ListKind: "NodePoolList",
							Plural:   "nodepools",
						},
						Scope: apiextensionsv1.NamespaceScoped,
						Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
							{Name: "v1beta1", Storage: true, Served: true},
						},
					},
				},
			},
			crs: []*unstructured.Unstructured{
				createUnstructuredCR("hypershift.openshift.io/v1beta1", "HostedCluster", "my-hc", "test-hc-ns", nil),
				createUnstructuredCR("hypershift.openshift.io/v1beta1", "NodePool", "my-nodepool-1", "test-hc-ns", nil),
				createUnstructuredCR("hypershift.openshift.io/v1beta1", "NodePool", "my-nodepool-2", "test-hc-ns", nil),
			},
			hostedClusterNamespace: "test-hc-ns",
			expectedCRListCount:    2,
			expectedTotalCRs:       3,
		},
		{
			name: "returns error when namespace missing cluster ID label",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-hc-ns",
					Labels: map[string]string{},
				},
			},
			crds:                   []*apiextensionsv1.CustomResourceDefinition{},
			crs:                    []*unstructured.Unstructured{},
			hostedClusterNamespace: "test-hc-ns",
			expectedError:          "namespace test-hc-ns missing label api.openshift.com/id",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			scheme := newTestScheme()

			for _, crd := range tc.crds {
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
			}

			var objs []client.Object
			if tc.namespace != nil {
				objs = append(objs, tc.namespace)
			}
			for _, crd := range tc.crds {
				objs = append(objs, crd)
			}
			for _, cr := range tc.crs {
				objs = append(objs, cr)
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
			lister := NewCustomResourceLister(fakeClient)
			ctx := context.Background()

			crChan := make(chan *unstructured.UnstructuredList)
			var receivedLists []*unstructured.UnstructuredList

			// Start goroutine to receive from channel
			done := make(chan struct{})
			go func() {
				for crList := range crChan {
					receivedLists = append(receivedLists, crList)
				}
				close(done)
			}()

			err := lister.StreamCRs(ctx, tc.hostedClusterNamespace, crChan)
			// close the consumer go routine channel.
			close(crChan)
			// Wait for the consumer to finish.
			<-done

			if tc.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
			} else {
				require.NoError(t, err)
				assert.Len(t, receivedLists, tc.expectedCRListCount)

				totalCRs := 0
				for _, crList := range receivedLists {
					totalCRs += len(crList.Items)
				}
				assert.Equal(t, tc.expectedTotalCRs, totalCRs)
			}
		})
	}
}

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

	// Custom output function that collects CRs
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
