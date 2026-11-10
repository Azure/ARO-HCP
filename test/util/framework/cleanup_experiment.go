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
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/onsi/ginkgo/v2"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/timing"
)

// directResourceGroupDeletionSpecsEnv names the environment variable that opts specs into
// deleting their customer resource group without an explicit HCP cluster delete. The value
// is a comma-separated list of case-insensitive spec name fragments. An empty value keeps
// every spec on the standard cleanup path.
const directResourceGroupDeletionSpecsEnv = "ARO_HCP_E2E_DIRECT_RG_DELETE_SPECS"

// directDeletionVerificationTimeout bounds how long the experiment waits for the resource
// group, its clusters and their managed resource groups to report not-found after the
// customer resource group delete completes.
const directDeletionVerificationTimeout = 10 * time.Minute

// Reports whether a spec opted into the direct resource group deletion experiment. Spec
// names and fragments are compared with all non-alphanumeric characters collapsed to single
// spaces, so "Cluster Etcd" matches "cluster-etcd".
func specSelectsDirectResourceGroupDeletion(specName, rawSpecs string) bool {
	normalizedSpec := normalizeSpecFragment(specName)
	if normalizedSpec == "" {
		return false
	}
	for _, raw := range strings.Split(rawSpecs, ",") {
		fragment := normalizeSpecFragment(raw)
		if fragment != "" && strings.Contains(normalizedSpec, fragment) {
			return true
		}
	}
	return false
}

// normalizeSpecFragment lowercases a name and collapses every run of non-alphanumeric
// characters into one space.
func normalizeSpecFragment(name string) string {
	var builder strings.Builder
	pendingSpace := false
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			pendingSpace = false
			continue
		}
		if !pendingSpace {
			builder.WriteByte(' ')
			pendingSpace = true
		}
	}
	return strings.TrimSpace(builder.String())
}

// classifyResourceLookup maps one GET result to "gone" or "still present". Only a typed
// 404 counts as gone. Permission, transport and other request failures stay fatal so the
// experiment cannot report a surviving resource as deleted.
func classifyResourceLookup(err error) (gone bool, fatal error) {
	if err == nil {
		return false, nil
	}
	var responseErr *azcore.ResponseError
	if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound {
		return true, nil
	}
	return false, err
}

// waitForResourceDisappearance polls every resource probe until each reports a typed
// not-found response or the timeout expires. Only a typed 404 counts as gone; permission
// and transport failures are fatal. When a lookup is interrupted by cancellation or the
// deadline, it returns the resources that were not verified, including those not yet
// re-checked in the interrupted poll, so a failure report always names the pending set.
func waitForResourceDisappearance(ctx context.Context, timeout, interval time.Duration, resources map[string]func(context.Context) error) ([]string, error) {
	if len(resources) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	names := make([]string, 0, len(resources))
	for name := range resources {
		names = append(names, name)
	}
	slices.Sort(names)

	deadline := time.Now().Add(timeout)
	for {
		pending := make([]string, 0, len(names))
		for i, name := range names {
			gone, fatal := classifyResourceLookup(resources[name](ctx))
			if fatal != nil {
				if ctx.Err() != nil {
					// The lookup itself was interrupted. Every resource from this one onward is
					// still unverified, and the earlier pending ones are known to be present.
					pending = append(pending, names[i:]...)
					return pending, fmt.Errorf("%d resources not verified before the context ended: %s: %w", len(pending), strings.Join(pending, ", "), ctx.Err())
				}
				return nil, fmt.Errorf("failed checking %s: %w", name, fatal)
			}
			if !gone {
				pending = append(pending, name)
			}
		}
		if len(pending) == 0 {
			return nil, nil
		}
		if !time.Now().Before(deadline) {
			return pending, fmt.Errorf("%d resources still present after %s: %s", len(pending), timeout, strings.Join(pending, ", "))
		}
		select {
		case <-ctx.Done():
			return pending, fmt.Errorf("%d resources still present when the context ended: %s: %w", len(pending), strings.Join(pending, ", "), ctx.Err())
		case <-time.After(interval):
		}
	}
}

// cleanupResourceGroupByDirectDeletion is the experimental cleanup path selected by
// ARO_HCP_E2E_DIRECT_RG_DELETE_SPECS. It inventories the clusters and their managed resource
// groups, deletes the customer resource group without an explicit cluster delete, then
// requires a typed not-found response for the resource group, every cluster and every
// managed resource group before purging soft-deleted vaults.
func (tc *perItOrDescribeTestContext) cleanupResourceGroupByDirectDeletion(ctx context.Context, resourceGroupName string, timeout time.Duration) (operationErr error) {
	startTime := time.Now()
	defer func() {
		tc.RecordTestOperation(timing.KindResourceGroupCleanup, fmt.Sprintf("Clean up resource group %s (direct delete)", resourceGroupName), startTime, time.Now(), operationErr)
	}()

	resourceClientFactory, err := tc.GetARMResourcesClientFactory(ctx)
	if err != nil {
		return err
	}
	networkClientFactory, err := tc.GetARMNetworkClientFactory(ctx)
	if err != nil {
		return err
	}
	hcpClientFactory, err := tc.Get20240610ClientFactory(ctx)
	if err != nil {
		return err
	}
	resourceGroupsClient := resourceClientFactory.NewResourceGroupsClient()
	clustersClient := hcpClientFactory.NewHcpOpenShiftClustersClient()

	inventory, err := listHCPClusters20240610(ctx, clustersClient, resourceGroupName)
	if err != nil && !isResourceGroupNotFoundError(err) {
		return fmt.Errorf("failed to inventory clusters in %s before direct deletion: %w", resourceGroupName, err)
	}
	if err != nil {
		// The customer resource group is already gone, so its clusters are gone too, but the
		// managed resource groups are owned separately and still need verification.
		ginkgo.GinkgoLogr.Info("customer resource group already deleted; verifying related resources only", "resourceGroup", resourceGroupName)
		inventory = nil
	}

	clusterNames := make([]string, 0, len(inventory))
	declaredManagedResourceGroups := make([]string, 0, len(inventory))
	for _, entry := range inventory {
		clusterNames = append(clusterNames, entry.Name)
		declaredManagedResourceGroups = append(declaredManagedResourceGroups, entry.ManagedResourceGroup)
	}
	// Managed resource groups are not always stamped with ManagedBy (Cluster Service can leave
	// them unclaimed) and can appear asynchronously, so the cluster-declared names are unioned
	// with subscription discovery to keep either gap from skipping a probe.
	discoveredManagedResourceGroups, err := tc.findManagedResourceGroups(ctx, resourceGroupName)
	if err != nil {
		return fmt.Errorf("failed to inventory managed resource groups in %s before direct deletion: %w", resourceGroupName, err)
	}
	managedResourceGroups := mergeResourceNames(declaredManagedResourceGroups, discoveredManagedResourceGroups)
	ginkgo.GinkgoLogr.Info("starting direct resource group deletion",
		"resourceGroup", resourceGroupName,
		"clusters", clusterNames,
		"managedResourceGroups", managedResourceGroups)

	// The delete has to start the teardown before anything waits for it, otherwise the
	// bounded verification below would poll resources nothing has asked Azure to remove.
	if err := tc.recordCleanupStage(fmt.Sprintf("Delete resource group %s", resourceGroupName), func() error {
		return DeleteResourceGroup(ctx, resourceGroupsClient, networkClientFactory, resourceGroupName, false, timeout)
	}); err != nil {
		return fmt.Errorf("failed to delete resource group %s directly: %w", resourceGroupName, err)
	}

	probes := map[string]func(context.Context) error{
		"resource group " + resourceGroupName: func(ctx context.Context) error {
			_, err := resourceGroupsClient.Get(ctx, resourceGroupName, nil)
			return err
		},
	}
	for _, clusterName := range clusterNames {
		clusterName := clusterName
		probes[fmt.Sprintf("cluster %s/%s", resourceGroupName, clusterName)] = func(ctx context.Context) error {
			_, err := clustersClient.Get(ctx, resourceGroupName, clusterName, nil)
			return err
		}
	}
	for _, managedResourceGroup := range managedResourceGroups {
		managedResourceGroup := managedResourceGroup
		probes["managed resource group "+managedResourceGroup] = func(ctx context.Context) error {
			_, err := resourceGroupsClient.Get(ctx, managedResourceGroup, nil)
			return err
		}
	}

	verifyErr := tc.recordCleanupStage(fmt.Sprintf("Verify resource disappearance in %s", resourceGroupName), func() error {
		_, err := waitForResourceDisappearance(ctx, directDeletionVerificationTimeout, StandardPollInterval, probes)
		return err
	})
	if verifyErr != nil {
		return fmt.Errorf("direct resource group deletion did not complete for %s, manual or RP-side recovery may be required: %w", resourceGroupName, verifyErr)
	}

	// Deleting the resource group only soft-deletes any Key Vaults and Managed HSMs it
	// contained; their globally-unique names stay reserved until purged.
	tc.purgeDeletedVaultsInResourceGroup(ctx, resourceGroupName)
	return nil
}

// hcpClusterInventoryEntry is the pre-deletion inventory of one HCP cluster: its name and
// the managed resource group declared by the cluster.
type hcpClusterInventoryEntry struct {
	Name                 string
	ManagedResourceGroup string
}

// listHCPClusters20240610 returns the sorted pre-deletion inventory of HCP clusters in a
// resource group.
func listHCPClusters20240610(ctx context.Context, clustersClient *hcpsdk20240610preview.HcpOpenShiftClustersClient, resourceGroupName string) ([]hcpClusterInventoryEntry, error) {
	var inventory []hcpClusterInventoryEntry
	pager := clustersClient.NewListByResourceGroupPager(resourceGroupName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, cluster := range page.Value {
			if cluster == nil || cluster.Name == nil {
				continue
			}
			entry := hcpClusterInventoryEntry{Name: *cluster.Name}
			if cluster.Properties != nil && cluster.Properties.Platform != nil && cluster.Properties.Platform.ManagedResourceGroup != nil {
				entry.ManagedResourceGroup = *cluster.Properties.Platform.ManagedResourceGroup
			}
			inventory = append(inventory, entry)
		}
	}
	slices.SortFunc(inventory, func(a, b hcpClusterInventoryEntry) int { return strings.Compare(a.Name, b.Name) })
	return inventory, nil
}

// mergeResourceNames returns the sorted, de-duplicated union of resource name lists,
// ignoring blank entries.
func mergeResourceNames(nameLists ...[]string) []string {
	seen := make(map[string]struct{})
	var merged []string
	for _, names := range nameLists {
		for _, name := range names {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			merged = append(merged, name)
		}
	}
	slices.Sort(merged)
	return merged
}
