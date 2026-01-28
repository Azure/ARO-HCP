package crdump

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	clusterIDLabelKey = "api.openshift.com/id"
)

const (
	ManifestWorkCRD   = "manifestworks.work.open-cluster-management.io"
	ManagedClusterCRD = "managedclusters.cluster.open-cluster-management.io"
)

type CustomResourceLister interface {
	ListCRDs(ctx context.Context) (*apiextensionsv1.CustomResourceDefinitionList, error)
	ListCRs(ctx context.Context, hostedClusterNamespace string) ([]*unstructured.UnstructuredList, error)
}

type customResourceLister struct {
	k8sclient client.Client
}

func NewCustomResourceLister(k8sclient client.Client) CustomResourceLister {
	return &customResourceLister{k8sclient: k8sclient}
}

func (l *customResourceLister) ListCRDs(ctx context.Context) (*apiextensionsv1.CustomResourceDefinitionList, error) {
	crdList := &apiextensionsv1.CustomResourceDefinitionList{}
	if err := l.k8sclient.List(ctx, crdList); err != nil {
		return nil, fmt.Errorf("failed to list CRDs: %w", err)
	}
	return crdList, nil
}

func (l *customResourceLister) ListCRs(ctx context.Context, hostedClusterNamespace string) ([]*unstructured.UnstructuredList, error) {
	clusterID, err := l.getClusterID(ctx, hostedClusterNamespace)
	if err != nil {
		return nil, err
	}

	crdList, err := l.ListCRDs(ctx)
	if err != nil {
		return nil, err
	}

	var allCRs []*unstructured.UnstructuredList
	for _, crd := range crdList.Items {
		crList, err := l.listCRsForCRD(ctx, &crd, hostedClusterNamespace, clusterID)
		if err != nil {
			return nil, fmt.Errorf("failed to list CRs for CRD %s: %w", crd.Name, err)
		}

		if len(crList.Items) > 0 {
			allCRs = append(allCRs, crList)
		}
	}

	return allCRs, nil
}

func (l *customResourceLister) listCRsForCRD(ctx context.Context, crd *apiextensionsv1.CustomResourceDefinition, namespace, clusterID string) (*unstructured.UnstructuredList, error) {
	version, err := getStorageVersion(crd)
	if err != nil {
		return nil, err
	}

	gvk := getGVK(crd, version)

	if !l.shouldFetchCR(crd, namespace, clusterID) {
		// Skip this CRD
		return &unstructured.UnstructuredList{}, nil
	}

	listOpts := l.getListOptions(crd, namespace, clusterID)
	crList, err := l.list(ctx, gvk, listOpts...)
	if err != nil {
		return nil, err
	}
	return crList, nil
}

func (l *customResourceLister) getListOptions(crd *apiextensionsv1.CustomResourceDefinition, namespace, clusterID string) []client.ListOption {
	switch crd.Name {
	case ManifestWorkCRD:
		return []client.ListOption{
			client.InNamespace("local-cluster"),
			// For manifestwork, filter by cluster ID label.
			client.MatchingLabels{clusterIDLabelKey: clusterID},
		}
	case ManagedClusterCRD:
		// For managed cluster cr, the name is of the resource is the cluster ID.
		return []client.ListOption{
			client.MatchingFields{"metadata.name": clusterID},
		}
	default:
		// For other crds, only list namespace-scoped resources.
		if isNamespaceScoped(crd) {
			return []client.ListOption{client.InNamespace(namespace)}
		}
		// cluster-scoped CRDs that aren't in the OCM API list
		return []client.ListOption{}
	}
}
func (l *customResourceLister) shouldFetchCR(crd *apiextensionsv1.CustomResourceDefinition, namespace, clusterID string) bool {
	// Check if this is a special OCM CRD
	switch crd.Name {
	case ManifestWorkCRD, ManagedClusterCRD:
		return true
	default:
		if isNamespaceScoped(crd) {
			return true
		}
		// cluster-scoped CRD that isn't in the OCM API list
		return false
	}
}

func (l *customResourceLister) getClusterID(ctx context.Context, namespace string) (string, error) {
	ns := &corev1.Namespace{}
	if err := l.k8sclient.Get(ctx, client.ObjectKey{Name: namespace}, ns); err != nil {
		return "", fmt.Errorf("failed to get namespace %s: %w", namespace, err)
	}

	clusterID, ok := ns.Labels[clusterIDLabelKey]
	if !ok {
		return "", fmt.Errorf("namespace %s missing label %s", namespace, clusterIDLabelKey)
	}

	return clusterID, nil
}

func (l *customResourceLister) list(ctx context.Context, gvk schema.GroupVersionKind, opts ...client.ListOption) (*unstructured.UnstructuredList, error) {
	crList := &unstructured.UnstructuredList{}
	crList.SetGroupVersionKind(gvk)

	if err := l.k8sclient.List(ctx, crList, opts...); err != nil {
		return nil, fmt.Errorf("failed to list %s: %w", gvk.Kind, err)
	}

	return crList, nil
}

// Helper fns
func getStorageVersion(crd *apiextensionsv1.CustomResourceDefinition) (string, error) {
	for _, v := range crd.Spec.Versions {
		if v.Storage {
			return v.Name, nil
		}
	}
	return "", fmt.Errorf("no storage version found for CRD %s", crd.Name)
}

func isNamespaceScoped(crd *apiextensionsv1.CustomResourceDefinition) bool {
	return crd.Spec.Scope == apiextensionsv1.NamespaceScoped
}

func getGVK(crd *apiextensionsv1.CustomResourceDefinition, version string) schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   crd.Spec.Group,
		Version: version,
		Kind:    crd.Spec.Names.Kind,
	}
}

// Output format: <outputDir>/crs/<namespace>/<kind>list.yaml
func WriteCRsToDisk(hostedClusterNamespace string, crLists []*unstructured.UnstructuredList, outputDir string) error {
	if len(crLists) == 0 {
		return nil
	}

	// Create output directory
	outputPath := filepath.Join(outputDir, "crs", hostedClusterNamespace)
	if err := os.MkdirAll(outputPath, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", outputPath, err)
	}

	// Write each CR list to a separate file
	for _, crList := range crLists {
		if len(crList.Items) == 0 {
			continue
		}

		kind := crList.Items[0].GetKind()
		filename := filepath.Join(outputPath, strings.ToLower(kind)+"list.yaml")

		fmt.Printf("Writing %d %s resources to %s\n", len(crList.Items), kind, filename)

		if err := writeYAMLFile(crList.Items, filename); err != nil {
			return fmt.Errorf("failed to write %s: %w", filename, err)
		}
	}

	return nil
}

func writeYAMLFile(items []unstructured.Unstructured, filename string) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	for i, item := range items {
		// Add seperator
		if i > 0 {
			if _, err := f.WriteString("---\n"); err != nil {
				return err
			}
		}
		data, err := yaml.Marshal(item.Object)
		if err != nil {
			return fmt.Errorf("failed to marshal %s/%s: %w", item.GetNamespace(), item.GetName(), err)
		}
		if _, err := f.Write(data); err != nil {
			return err
		}
	}
	return nil
}
