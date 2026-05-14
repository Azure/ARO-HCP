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

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

const apiGroup = "package-operator.run"

type crdInfo struct {
	Name    string
	Plural  string
	Group   string
	Version string
	Scope   apiextensionsv1.ResourceScope
}

func main() {
	timeout := 10 * time.Minute
	if v := os.Getenv("PKO_CLEANUP_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			timeout = d
		} else {
			fmt.Fprintf(os.Stderr, "[WARNING] failed to parse PKO_CLEANUP_TIMEOUT=%q: %v, using default %s\n", v, err, timeout)
		}
	}

	if err := run(timeout); err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
		fmt.Printf("\n=== PKO cleanup completed with 1 error(s) (best-effort, not blocking rollout) ===\n")
		return
	}
}

func run(timeout time.Duration) error {
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(), nil,
	).ClientConfig()
	if err != nil {
		return fmt.Errorf("building kubeconfig: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("creating dynamic client: %w", err)
	}

	crdClient, err := apiextensionsclient.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("creating CRD client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var errors int

	fmt.Println("=== Package Operator CR + CRD cleanup (best-effort) ===")

	crds, err := discoverPKOCRDs(ctx, crdClient)
	if err != nil {
		return fmt.Errorf("discovering CRDs: %w", err)
	}
	if len(crds) == 0 {
		fmt.Println("No package-operator.run CRDs found. Nothing to do.")
		fmt.Println("\n=== PKO resource cleanup complete ===")
		return nil
	}

	fmt.Printf("Found %d CRD(s):\n", len(crds))
	for _, c := range crds {
		fmt.Printf("  %s (%s)\n", c.Name, c.Scope)
	}

	errors += deleteCRs(ctx, dynamicClient, crds, timeout)

	remaining := waitForDeletion(ctx, dynamicClient, crds, 180*time.Second)

	if remaining < 0 {
		fmt.Fprintf(os.Stderr, "[WARNING] unable to determine CR count after waiting — removing finalizers as precaution.\n")
		errors++
	} else if remaining > 0 {
		fmt.Fprintf(os.Stderr, "[WARNING] %d CR(s) stuck after 180s — removing finalizers.\n", remaining)
	}
	if remaining != 0 {
		errors += stripFinalizers(ctx, dynamicClient, crds, timeout)

		time.Sleep(10 * time.Second)

		remaining = countAllCRs(ctx, dynamicClient, crds)
		if remaining < 0 {
			fmt.Fprintf(os.Stderr, "[ERROR] unable to determine remaining CR count after finalizer removal\n")
			errors++
		} else if remaining > 0 {
			fmt.Fprintf(os.Stderr, "[ERROR] %d CR(s) still remain after finalizer removal\n", remaining)
			errors++
		} else {
			fmt.Println("All stuck CRs removed.")
		}
	}

	errors += deleteCRDs(ctx, crdClient, crds, timeout)

	if errors > 0 {
		fmt.Printf("\n=== PKO cleanup completed with %d error(s) (best-effort, not blocking rollout) ===\n", errors)
	} else {
		fmt.Println("\n=== PKO resource cleanup complete ===")
	}
	return nil
}

func discoverPKOCRDs(ctx context.Context, client apiextensionsclient.Interface) ([]crdInfo, error) {
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	list, err := client.ApiextensionsV1().CustomResourceDefinitions().List(listCtx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var result []crdInfo
	for _, crd := range list.Items {
		if !isPKOGroup(crd.Spec.Group) {
			continue
		}
		result = append(result, crdInfo{
			Name:    crd.Name,
			Plural:  crd.Spec.Names.Plural,
			Group:   crd.Spec.Group,
			Version: storageVersion(crd),
			Scope:   crd.Spec.Scope,
		})
	}
	return result, nil
}

func isPKOGroup(group string) bool {
	return group == apiGroup || strings.HasSuffix(group, "."+apiGroup)
}

func storageVersion(crd apiextensionsv1.CustomResourceDefinition) string {
	for _, v := range crd.Spec.Versions {
		if v.Storage {
			return v.Name
		}
	}
	if len(crd.Spec.Versions) > 0 {
		return crd.Spec.Versions[0].Name
	}
	return "v1alpha1"
}

func gvr(c crdInfo) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: c.Group, Version: c.Version, Resource: c.Plural}
}

func deleteCRs(ctx context.Context, client dynamic.Interface, crds []crdInfo, timeout time.Duration) int {
	errors := 0
	for _, c := range crds {
		crdCtx, cancel := context.WithTimeout(ctx, timeout)
		resource := fmt.Sprintf("%s.%s", c.Plural, c.Group)
		fmt.Printf("\n--- Deleting all %s CRs (%s) ---\n", resource, c.Scope)

		deletePolicy := metav1.DeletePropagationBackground
		opts := metav1.DeleteOptions{
			PropagationPolicy: &deletePolicy,
		}

		if c.Scope == apiextensionsv1.NamespaceScoped {
			list, err := client.Resource(gvr(c)).Namespace("").List(crdCtx, metav1.ListOptions{})
			if err != nil {
				fmt.Fprintf(os.Stderr, "[ERROR] failed to list %s: %v\n", resource, err)
				errors++
				cancel()
				continue
			}

			namespaces := make(map[string]struct{})
			for _, item := range list.Items {
				namespaces[item.GetNamespace()] = struct{}{}
			}

			for ns := range namespaces {
				if err := client.Resource(gvr(c)).Namespace(ns).DeleteCollection(crdCtx, opts, metav1.ListOptions{}); err != nil {
					fmt.Fprintf(os.Stderr, "[ERROR] failed to delete %s in namespace %s: %v\n", resource, ns, err)
					errors++
				}
			}
		} else {
			if err := client.Resource(gvr(c)).DeleteCollection(crdCtx, opts, metav1.ListOptions{}); err != nil {
				fmt.Fprintf(os.Stderr, "[ERROR] failed to delete %s: %v\n", resource, err)
				errors++
			}
		}
		cancel()
	}
	return errors
}

func countCRs(ctx context.Context, client dynamic.Interface, c crdInfo) int {
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var list *unstructured.UnstructuredList
	var err error

	if c.Scope == apiextensionsv1.NamespaceScoped {
		list, err = client.Resource(gvr(c)).Namespace("").List(listCtx, metav1.ListOptions{})
	} else {
		list, err = client.Resource(gvr(c)).List(listCtx, metav1.ListOptions{})
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] failed to list %s.%s: %v\n", c.Plural, c.Group, err)
		return -1
	}
	return len(list.Items)
}

func countAllCRs(ctx context.Context, client dynamic.Interface, crds []crdInfo) int {
	total := 0
	for _, c := range crds {
		n := countCRs(ctx, client, c)
		if n < 0 {
			return -1
		}
		total += n
	}
	return total
}

func waitForDeletion(ctx context.Context, client dynamic.Interface, crds []crdInfo, maxWait time.Duration) int {
	fmt.Println("\nWaiting for cascading deletion to complete...")

	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		remaining := countAllCRs(ctx, client, crds)
		if remaining == 0 {
			fmt.Println("All package-operator CRs have been deleted.")
			return 0
		}
		if remaining < 0 {
			fmt.Println("  Unable to determine CR count, retrying...")
		} else {
			elapsed := time.Since(deadline.Add(-maxWait)).Truncate(time.Second)
			fmt.Printf("  %d CR(s) still remaining, waiting... (%s / %s)\n",
				remaining, elapsed, maxWait)
		}
		time.Sleep(10 * time.Second)
	}

	remaining := countAllCRs(ctx, client, crds)
	if remaining < 0 {
		return -1
	}
	return remaining
}

func stripFinalizers(ctx context.Context, client dynamic.Interface, crds []crdInfo, timeout time.Duration) int {
	patch := []byte(`{"metadata":{"finalizers":[]}}`)
	errors := 0

	for _, c := range crds {
		crdCtx, cancel := context.WithTimeout(ctx, timeout)
		errors += stripFinalizersForCRD(crdCtx, client, c, patch)
		cancel()
	}
	return errors
}

func stripFinalizersForCRD(ctx context.Context, client dynamic.Interface, c crdInfo, patch []byte) int {
	resource := fmt.Sprintf("%s.%s", c.Plural, c.Group)
	errors := 0

	var list *unstructured.UnstructuredList
	var err error
	if c.Scope == apiextensionsv1.NamespaceScoped {
		list, err = client.Resource(gvr(c)).Namespace("").List(ctx, metav1.ListOptions{})
	} else {
		list, err = client.Resource(gvr(c)).List(ctx, metav1.ListOptions{})
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] failed to list %s for finalizer removal: %v\n", resource, err)
		return 1
	}

	for _, item := range list.Items {
		if len(item.GetFinalizers()) == 0 {
			continue
		}
		if item.GetDeletionTimestamp() == nil {
			if ns := item.GetNamespace(); ns != "" {
				fmt.Printf("  Skipping %s/%s -n %s — not being deleted\n", resource, item.GetName(), ns)
			} else {
				fmt.Printf("  Skipping %s/%s — not being deleted\n", resource, item.GetName())
			}
			continue
		}

		ns := item.GetNamespace()
		name := item.GetName()
		if ns != "" {
			fmt.Printf("  Patching finalizers on %s/%s -n %s\n", resource, name, ns)
			_, err = client.Resource(gvr(c)).Namespace(ns).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
		} else {
			fmt.Printf("  Patching finalizers on %s/%s\n", resource, name)
			_, err = client.Resource(gvr(c)).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
		}
		if err != nil && !apierrors.IsNotFound(err) {
			fmt.Fprintf(os.Stderr, "[ERROR] failed to patch finalizers on %s/%s: %v\n", resource, name, err)
			errors++
		}
	}
	return errors
}

func deleteCRDs(ctx context.Context, client apiextensionsclient.Interface, crds []crdInfo, timeout time.Duration) int {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	fmt.Println("\nRemoving package-operator.run CRDs...")

	errors := 0
	for _, c := range crds {
		fmt.Printf("  Deleting CRD: %s\n", c.Name)
		if err := client.ApiextensionsV1().CustomResourceDefinitions().Delete(ctx, c.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			fmt.Fprintf(os.Stderr, "[ERROR] failed to delete CRD %s: %v\n", c.Name, err)
			errors++
		}
	}
	return errors
}
