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

package clients

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/upgrade"
)

func newAzureCredential() (*azidentity.DefaultAzureCredential, error) {
	return azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
}

// ListAKSMeshRevisions returns the AKS-default ASM revision for the given
// location as a single-element slice. Overridable in tests. Called once per
// configured location by the updater; the updater intersects results across
// locations so the tool only advances when every region agrees on the default.
//
// ARM's MeshRevisionProfiles API does not surface a `default` flag, so we
// derive it: the default is the highest revision whose `Upgrades` list is
// non-empty (i.e. the newest revision that is not the bleeding-edge). This
// matches what AKS actually installs on a fresh cluster.
var ListAKSMeshRevisions func(ctx context.Context, subscriptionID, location string) ([]string, error) = defaultListAKSMeshRevisions

func defaultListAKSMeshRevisions(ctx context.Context, subscriptionID, location string) ([]string, error) {
	if subscriptionID == "" {
		return nil, fmt.Errorf("aks mesh revisions: subscription ID is required")
	}
	if location == "" {
		return nil, fmt.Errorf("aks mesh revisions: location is required")
	}

	cred, err := newAzureCredential()
	if err != nil {
		return nil, fmt.Errorf("aks mesh revisions: create azure credential: %w", err)
	}

	// ARM requires a subscription in the URL even though the mesh revision
	// data is product-wide and identical across all subscriptions.
	client, err := armcontainerservice.NewManagedClustersClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("aks mesh revisions: create managed clusters client: %w", err)
	}

	var upgradable []string
	var all []string
	pager := client.NewListMeshRevisionProfilesPager(location, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("aks mesh revisions: list profiles in %s: %w", location, err)
		}
		for _, profile := range page.Value {
			if profile == nil || profile.Properties == nil {
				continue
			}
			for _, rev := range profile.Properties.MeshRevisions {
				if rev == nil || rev.Revision == nil || *rev.Revision == "" {
					continue
				}
				all = append(all, *rev.Revision)
				if len(rev.Upgrades) > 0 {
					upgradable = append(upgradable, *rev.Revision)
				}
			}
		}
	}

	defaultRev, err := pickDefaultMeshRevision(all, upgradable, location)
	if err != nil {
		return nil, err
	}
	return []string{defaultRev}, nil
}

// ResolveSubscription returns the first enabled subscription visible to the
// current Azure credentials. This avoids requiring a hardcoded subscription
// ID when the mesh revision profiles API data is product-wide anyway.
func ResolveSubscription(ctx context.Context) (string, error) {
	cred, err := newAzureCredential()
	if err != nil {
		return "", fmt.Errorf("resolve subscription: create credential: %w", err)
	}
	client, err := armsubscriptions.NewClient(cred, nil)
	if err != nil {
		return "", fmt.Errorf("resolve subscription: create client: %w", err)
	}
	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("resolve subscription: list: %w", err)
		}
		for _, sub := range page.Value {
			if sub != nil && sub.State != nil && *sub.State == armsubscriptions.SubscriptionStateEnabled && sub.SubscriptionID != nil {
				return *sub.SubscriptionID, nil
			}
		}
	}
	return "", fmt.Errorf("resolve subscription: no enabled subscriptions found for the current credentials")
}

// pickDefaultMeshRevision returns the AKS-default revision for a location given
// the full catalogue and the subset with a non-empty Upgrades list. Extracted
// for unit testing without an ARM mock.
//
// ARM does not expose an explicit "default" flag on mesh revisions. AKS
// currently treats the highest revision whose Upgrades list is non-empty as the
// default installed on new clusters. This heuristic is based on observed ARM
// responses and should be revisited if the API changes.
//
// Rule: pick the highest revision that still has upgrades available. Fall back
// to the highest revision overall when only the bleeding-edge is listed
// (single-revision catalogue during a fresh mesh rollout).
func pickDefaultMeshRevision(all, upgradable []string, location string) (string, error) {
	if len(all) == 0 {
		return "", fmt.Errorf("aks mesh revisions: no revisions returned for location %s", location)
	}
	pick := upgradable
	if len(pick) == 0 {
		pick = all
	}
	highest, err := upgrade.HighestAsmRevision(pick)
	if err != nil {
		return "", fmt.Errorf("aks mesh revisions: pick default in %s: %w", location, err)
	}
	return highest, nil
}
