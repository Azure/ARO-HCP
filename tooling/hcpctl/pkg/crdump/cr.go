package crdump

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sync/errgroup"
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
	ManifestWorkCRD       = "manifestworks.work.open-cluster-management.io"
	ManagedClusterCRD     = "managedclusters.cluster.open-cluster-management.io"
	ManagedClusterInfoCRD = "managedclusterinfos.internal.open-cluster-management.io"
)

// - "outputPath": base directory for output files
// - "hostedClusterNamespace": namespace being dumped
type CROutputOptions struct {
	OutputPath             string
	HostedClusterNamespace string
}

// CROutputFunc defines how custom resources should be processed and output.
// This function receives CR data through crChan and should process it according
// to the configuration in options.
type CROutputFunc func(crChan <-chan *unstructured.UnstructuredList, options CROutputOptions) error

type Dumper struct {
	lister        CustomResourceLister
	outputFunc    CROutputFunc
	outputOptions CROutputOptions
}

// NewDumper creates a new Dumper with custom output function and options.
// This constructor provides full control over how CR data is processed and output.
func NewDumper(lister CustomResourceLister, outputFunc CROutputFunc, outputOptions CROutputOptions) *Dumper {
	return &Dumper{
		lister:        lister,
		outputFunc:    outputFunc,
		outputOptions: outputOptions,
	}
}

// NewCliDumper creates a new Dumper with file-based output for CLI usage.
func NewCliDumper(lister CustomResourceLister, outputPath, hostedClusterNamespace string) *Dumper {
	outputOptions := CROutputOptions{
		OutputPath:             outputPath,
		HostedClusterNamespace: hostedClusterNamespace,
	}

	return &Dumper{
		lister:        lister,
		outputFunc:    cliOutputFunc,
		outputOptions: outputOptions,
	}
}

func cliOutputFunc(crChan <-chan *unstructured.UnstructuredList, options CROutputOptions) error {
	outputPath := options.OutputPath
	hostedClusterNamespace := options.HostedClusterNamespace

	outputDir := filepath.Join(outputPath, "crs", hostedClusterNamespace)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", outputDir, err)
	}

	openedFiles := make(map[string]*os.File)
	var allErrors error

	defer func() {
		for _, file := range openedFiles {
			if closeErr := file.Close(); closeErr != nil {
				allErrors = errors.Join(allErrors, fmt.Errorf("failed to close file: %w", closeErr))
			}
		}
	}()

	for crList := range crChan {
		if len(crList.Items) == 0 {
			continue
		}

		kind := crList.Items[0].GetKind()
		filename := filepath.Join(outputDir, strings.ToLower(kind)+"_list.yaml")

		file, ok := openedFiles[filename]
		if !ok {
			newFile, err := os.Create(filename)
			if err != nil {
				allErrors = errors.Join(allErrors, fmt.Errorf("failed to create output file %s: %w", filename, err))
				continue
			}
			openedFiles[filename] = newFile
			file = newFile
		}

		for i, item := range crList.Items {
			if i > 0 {
				if _, err := file.WriteString("---\n"); err != nil {
					allErrors = errors.Join(allErrors, fmt.Errorf("failed to write separator: %w", err))
					continue
				}
			}
			data, err := yaml.Marshal(item.Object)
			if err != nil {
				allErrors = errors.Join(allErrors, fmt.Errorf("failed to marshal %s/%s: %w", item.GetNamespace(), item.GetName(), err))
				continue
			}
			if _, err := file.Write(data); err != nil {
				allErrors = errors.Join(allErrors, fmt.Errorf("failed to write to file: %w", err))
			}
		}
	}

	return allErrors
}

// DumpCRs lists all custom resources for the given namespace and streams them through the output function.
func (d *Dumper) DumpCRs(ctx context.Context, hostedClusterNamespace string) error {
	crChan := make(chan *unstructured.UnstructuredList)

	outputFnGroup := new(errgroup.Group)

	// Call output fn in a separate goroutine.
	outputFnGroup.Go(func() error {
		return d.outputFunc(crChan, d.outputOptions)
	})

	// List CRs and send to channel
	listErr := d.lister.StreamCRs(ctx, hostedClusterNamespace, crChan)

	// Close channel to signal completion to output fn
	close(crChan)

	// Wait for output fn to complete
	outputErr := outputFnGroup.Wait()

	if listErr != nil {
		return fmt.Errorf("failed to list CRs: %w", listErr)
	}
	if outputErr != nil {
		return fmt.Errorf("failed to output CRs: %w", outputErr)
	}

	return nil
}

type CustomResourceLister interface {
	ListCRDs(ctx context.Context) (*apiextensionsv1.CustomResourceDefinitionList, error)
	ListCRs(ctx context.Context, hostedClusterNamespace string) ([]*unstructured.UnstructuredList, error)
	StreamCRs(ctx context.Context, hostedClusterNamespace string, crChan chan<- *unstructured.UnstructuredList) error
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

// StreamCRs lists all custom resources and streams them through the provided channel.
// The caller is responsible for closing the channel after this method returns.
func (l *customResourceLister) StreamCRs(ctx context.Context, hostedClusterNamespace string, crChan chan<- *unstructured.UnstructuredList) error {
	clusterID, err := l.getClusterID(ctx, hostedClusterNamespace)
	if err != nil {
		return err
	}

	crdList, err := l.ListCRDs(ctx)
	if err != nil {
		return err
	}

	for _, crd := range crdList.Items {
		crList, err := l.listCRsForCRD(ctx, &crd, hostedClusterNamespace, clusterID)
		if err != nil {
			return fmt.Errorf("failed to list CRs for CRD %s: %w", crd.Name, err)
		}

		if len(crList.Items) > 0 {
			crChan <- crList
		}
	}

	return nil
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
	case ManagedClusterInfoCRD:
		// managed cluster info cr resides in the namespace named after the cluster ID.
		return []client.ListOption{
			client.InNamespace(clusterID),
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
	case ManifestWorkCRD, ManagedClusterCRD, ManagedClusterInfoCRD:
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
		return "", fmt.Errorf("failed to get namespace '%s': %w", namespace, err)
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
