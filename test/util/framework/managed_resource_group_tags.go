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

package framework

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/onsi/ginkgo/v2"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// hcpClusterResourceType appears in every compiled template that creates an HCP
// cluster, including clusters declared in nested Bicep modules.
const hcpClusterResourceType = "microsoft.redhatopenshift/hcpopenshiftclusters"

// prowJobID returns the Prow job ID, or an empty string outside Prow. It matches the
// value CreateResourceGroup writes to ProwJobIDTag, so every resource group of a job
// carries the same tag value.
func prowJobID() string {
	jobID := os.Getenv("BUILD_ID")
	if strings.TrimSpace(jobID) == "" {
		return ""
	}
	return jobID
}

// jobIDTagPatch returns the tag to merge onto a resource group, or nil when the
// group already carries jobID or there is no job ID. A group attributed to another
// job is an error, so one job's spend is never moved to a different job. Azure tag
// names are case-insensitive, so an existing key in any casing counts.
func jobIDTagPatch(existingTags map[string]*string, jobID string) (map[string]*string, error) {
	if jobID == "" {
		return nil, nil
	}
	for key, existing := range existingTags {
		if !strings.EqualFold(key, ProwJobIDTag) || existing == nil {
			continue
		}
		if *existing == jobID {
			return nil, nil
		}
		return nil, fmt.Errorf("already attributed to job %q", *existing)
	}
	return map[string]*string{ProwJobIDTag: to.Ptr(jobID)}, nil
}

// managedByHCPClusterIn reports whether managedBy names an HCP cluster in the parent
// resource group, i.e. whether the group is that cluster's managed resource group.
func managedByHCPClusterIn(managedBy *string, parentResourceGroupName string) bool {
	if managedBy == nil {
		return false
	}
	return strings.Contains(
		strings.ToLower(*managedBy),
		strings.ToLower("/resourceGroups/"+parentResourceGroupName+"/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/"),
	)
}

// deploysHCPCluster reports whether a compiled ARM template declares an HCP cluster.
func deploysHCPCluster(template []byte) bool {
	return bytes.Contains(bytes.ToLower(template), []byte(hcpClusterResourceType))
}

// tagManagedResourceGroupsWithJobID tags the managed resource groups of the HCP
// clusters in parentResourceGroupName with the Prow job ID. The RP creates these
// groups without tags; tagging them lets Cost Management tag inheritance attribute
// the worker VMs and disks inside to this job. It is best effort: failures are
// logged and never fail the test.
func (tc *perItOrDescribeTestContext) tagManagedResourceGroupsWithJobID(ctx context.Context, parentResourceGroupName string) {
	jobID := prowJobID()
	if jobID == "" {
		return
	}
	logger := ginkgo.GinkgoLogr.WithValues("parentResourceGroup", parentResourceGroupName, "tag", ProwJobIDTag, "jobID", jobID)
	// A resource group this test did not create may be shared across jobs; attributing
	// its clusters to this job would misstate both jobs' spend.
	if !tc.createdResourceGroup(parentResourceGroupName) {
		return
	}

	managedResourceGroups, err := tc.listManagedResourceGroups(ctx, parentResourceGroupName)
	if err != nil {
		logger.Info("skipping cost attribution tags: failed to list managed resource groups", "error", err.Error())
		return
	}
	if len(managedResourceGroups) == 0 {
		return
	}

	clientFactory, err := tc.GetARMResourcesClientFactory(ctx)
	if err != nil {
		logger.Info("skipping cost attribution tags: failed to get ARM client", "error", err.Error())
		return
	}
	tagsClient := clientFactory.NewTagsClient()
	for _, resourceGroup := range managedResourceGroups {
		if resourceGroup.ID == nil || resourceGroup.Name == nil {
			continue
		}
		patch, err := jobIDTagPatch(resourceGroup.Tags, jobID)
		if err != nil {
			logger.Info("not tagging managed resource group", "managedResourceGroup", *resourceGroup.Name, "reason", err.Error())
			continue
		}
		if patch == nil {
			continue
		}
		if _, err := tagsClient.UpdateAtScope(ctx, *resourceGroup.ID, armresources.TagsPatchResource{
			Operation:  to.Ptr(armresources.TagsPatchOperationMerge),
			Properties: &armresources.Tags{Tags: patch},
		}, nil); err != nil {
			logger.Info("failed to tag managed resource group", "managedResourceGroup", *resourceGroup.Name, "error", err.Error())
			continue
		}
		logger.Info("tagged managed resource group for cost attribution", "managedResourceGroup", *resourceGroup.Name)
	}
}

// createdResourceGroup reports whether this test context created the resource group.
func (tc *perItOrDescribeTestContext) createdResourceGroup(resourceGroupName string) bool {
	tc.contextLock.RLock()
	defer tc.contextLock.RUnlock()
	return slices.ContainsFunc(tc.knownResourceGroups, func(known string) bool {
		return strings.EqualFold(known, resourceGroupName)
	})
}

// hasJobIDTag reports whether tags attribute a resource group to jobID.
func hasJobIDTag(tags map[string]*string, jobID string) bool {
	for key, value := range tags {
		if strings.EqualFold(key, ProwJobIDTag) && value != nil && *value == jobID {
			return true
		}
	}
	return false
}

// parseHCPClusterPath returns the subscription and resource group of an HCP cluster
// resource path, and false for any other path, including cluster sub-resources.
func parseHCPClusterPath(path string) (subscriptionID, resourceGroupName string, ok bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 8 ||
		!strings.EqualFold(parts[0], "subscriptions") ||
		!strings.EqualFold(parts[2], "resourceGroups") ||
		!strings.EqualFold(parts[4], "providers") ||
		!strings.EqualFold(parts[5]+"/"+parts[6], hcpClusterResourceType) {
		return "", "", false
	}
	return parts[1], parts[3], true
}

// succeededManagedResourceGroup returns the managed resource group of a cluster the
// RP reports as Succeeded. The response body remains readable by the caller.
func succeededManagedResourceGroup(resp *http.Response) (string, bool) {
	body, err := runtime.Payload(resp)
	if err != nil {
		return "", false
	}
	var cluster struct {
		Properties struct {
			ProvisioningState string `json:"provisioningState"`
			Platform          struct {
				ManagedResourceGroup string `json:"managedResourceGroup"`
			} `json:"platform"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(body, &cluster); err != nil ||
		!strings.EqualFold(cluster.Properties.ProvisioningState, "Succeeded") ||
		cluster.Properties.Platform.ManagedResourceGroup == "" {
		return "", false
	}
	return cluster.Properties.Platform.ManagedResourceGroup, true
}

type managedResourceGroupTagger interface {
	tag(ctx context.Context, subscriptionID, parentResourceGroupName, managedResourceGroupName string) (bool, error)
}

// managedResourceGroupJobIDPolicy tags the managed resource group of each HCP cluster
// the first time the RP reports the cluster Succeeded. It runs on every RP client, so
// a cluster is tagged however a test creates it, and its workers are attributed to
// the job for their whole lifetime.
type managedResourceGroupJobIDPolicy struct {
	tagger  managedResourceGroupTagger
	handled sync.Map
}

func (p *managedResourceGroupJobIDPolicy) Do(req *policy.Request) (*http.Response, error) {
	resp, err := req.Next()
	if err != nil || resp == nil || resp.StatusCode != http.StatusOK || req.Raw().Method != http.MethodGet {
		return resp, err
	}
	subscriptionID, parentResourceGroupName, ok := parseHCPClusterPath(req.Raw().URL.Path)
	if !ok {
		return resp, err
	}
	managedResourceGroupName, ok := succeededManagedResourceGroup(resp)
	if !ok {
		return resp, err
	}
	key := strings.ToLower(subscriptionID + "/" + managedResourceGroupName)
	if _, handled := p.handled.LoadOrStore(key, struct{}{}); handled {
		return resp, err
	}

	logger := ginkgo.GinkgoLogr.WithValues("parentResourceGroup", parentResourceGroupName, "managedResourceGroup", managedResourceGroupName, "tag", ProwJobIDTag)
	tagged, tagErr := p.tagger.tag(req.Raw().Context(), subscriptionID, parentResourceGroupName, managedResourceGroupName)
	switch {
	case tagErr != nil:
		// A later read of the cluster retries.
		p.handled.Delete(key)
		logger.Info("failed to tag managed resource group", "error", tagErr.Error())
	case tagged:
		logger.Info("tagged managed resource group for cost attribution")
	}
	return resp, err
}

// jobManagedResourceGroupTagger tags a managed resource group with the job ID when
// its cluster's parent resource group belongs to this job, as recorded by
// CreateResourceGroup. A resource group shared across jobs is never tagged.
type jobManagedResourceGroupTagger struct {
	jobID         string
	clientFactory func(subscriptionID string) (*armresources.ClientFactory, error)
}

func (t *jobManagedResourceGroupTagger) tag(ctx context.Context, subscriptionID, parentResourceGroupName, managedResourceGroupName string) (bool, error) {
	clientFactory, err := t.clientFactory(subscriptionID)
	if err != nil {
		return false, err
	}
	resourceGroupsClient := clientFactory.NewResourceGroupsClient()

	parent, err := resourceGroupsClient.Get(ctx, parentResourceGroupName, nil)
	if err != nil {
		return false, fmt.Errorf("failed reading parent resource group: %w", err)
	}
	if !hasJobIDTag(parent.Tags, t.jobID) {
		return false, nil
	}

	managed, err := resourceGroupsClient.Get(ctx, managedResourceGroupName, nil)
	if err != nil {
		return false, fmt.Errorf("failed reading managed resource group: %w", err)
	}
	if managed.ID == nil || !managedByHCPClusterIn(managed.ManagedBy, parentResourceGroupName) {
		return false, nil
	}
	patch, err := jobIDTagPatch(managed.Tags, t.jobID)
	if err != nil {
		ginkgo.GinkgoLogr.Info("not tagging managed resource group", "managedResourceGroup", managedResourceGroupName, "reason", err.Error())
		return false, nil
	}
	if patch == nil {
		return false, nil
	}
	if _, err := clientFactory.NewTagsClient().UpdateAtScope(ctx, *managed.ID, armresources.TagsPatchResource{
		Operation:  to.Ptr(armresources.TagsPatchOperationMerge),
		Properties: &armresources.Tags{Tags: patch},
	}, nil); err != nil {
		return false, fmt.Errorf("failed tagging managed resource group: %w", err)
	}
	return true, nil
}
