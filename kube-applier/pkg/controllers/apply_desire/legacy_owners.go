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

package apply_desire

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var (
	manifestWorksGVR        = schema.GroupVersionResource{Group: "work.open-cluster-management.io", Version: "v1", Resource: "manifestworks"}
	managedClusterAddonsGVR = schema.GroupVersionResource{Group: "addon.open-cluster-management.io", Version: "v1alpha1", Resource: "managedclusteraddons"}
)

// clusterIDFromNamespace is only called after the namespace grammar has been
// validated by RelateHostedClusterNamespace.
func clusterIDFromNamespace(namespace string) string {
	return namespace[strings.LastIndexByte(namespace, '-')+1:]
}

// listAll reads every page. A partial list is never evidence that a legacy
// owner is absent.
func listAll(ctx context.Context, resource dynamic.ResourceInterface) ([]unstructured.Unstructured, error) {
	var all []unstructured.Unstructured
	continueToken := ""
	for {
		page, err := resource.List(ctx, metav1.ListOptions{Limit: 500, Continue: continueToken})
		if err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		continueToken = page.GetContinue()
		if continueToken == "" {
			return all, nil
		}
	}
}

// legacyOwnerBlock reports a live ManifestWork or ManagedClusterAddon that may
// still publish the target. Maestro itself no longer runs in the target
// deployment, but a surviving OCM work object must still block cleanup.
//
// The OCM APIs are optional. ARO-HCP removes ACM from its management clusters:
// mgmt-agent's acm-uninstall controller deletes every CRD whose name ends in
// .open-cluster-management.io. Once that has run, the API server answers these
// lists with 404 "the server could not find the requested resource", which
// means the type is not served and therefore no object of that type can exist.
// Treating that as a failed lookup would block finalization forever, so a
// missing type is a valid "no legacy owners" answer. Any other read failure
// still blocks: it is not evidence of absence.
func (c *ApplyDesireController) legacyOwnerBlock(ctx context.Context, hostedClusterNamespace, controlPlaneNamespace string) (string, error) {
	clusterID := clusterIDFromNamespace(hostedClusterNamespace)
	works, err := listAll(ctx, c.dyn.Resource(manifestWorksGVR).Namespace("local-cluster"))
	if err != nil {
		if !ocmAPITypeAbsent(err) {
			return "", fmt.Errorf("list ManifestWorks: %w", err)
		}
		works = nil
	}
	for _, work := range works {
		name := work.GetName()
		if name == clusterID || strings.HasPrefix(name, clusterID+"-") ||
			manifestWorkMentionsNamespace(&work, hostedClusterNamespace, controlPlaneNamespace) {
			return fmt.Sprintf("ManifestWork local-cluster/%s still references this cluster", name), nil
		}
	}

	addons, err := listAll(ctx, c.dyn.Resource(managedClusterAddonsGVR).Namespace(clusterID))
	if err != nil {
		if !ocmAPITypeAbsent(err) {
			return "", fmt.Errorf("list ManagedClusterAddons in %s: %w", clusterID, err)
		}
		addons = nil
	}
	if len(addons) > 0 {
		return fmt.Sprintf("ManagedClusterAddon %s/%s still exists", clusterID, addons[0].GetName()), nil
	}
	return "", nil
}

// ocmAPITypeAbsent reports whether err means this cluster does not serve the
// OCM API type at all, as opposed to a transient failure to read it. Only the
// former is evidence that no legacy owner object exists.
func ocmAPITypeAbsent(err error) bool {
	return apierrors.IsNotFound(err) || meta.IsNoMatchError(err)
}

func manifestWorkMentionsNamespace(work *unstructured.Unstructured, hostedClusterNamespace, controlPlaneNamespace string) bool {
	manifests, found, err := unstructured.NestedSlice(work.Object, "spec", "workload", "manifests")
	if err != nil || !found {
		// An unreadable manifest cannot be safely ruled out as an owner.
		return err != nil
	}
	for _, manifest := range manifests {
		content, err := json.Marshal(manifest)
		if err != nil || strings.Contains(string(content), hostedClusterNamespace) ||
			strings.Contains(string(content), controlPlaneNamespace) {
			return true
		}
	}
	return false
}
