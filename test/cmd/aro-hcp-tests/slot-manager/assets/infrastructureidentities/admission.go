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

package infrastructureidentities

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets/azureutil"
)

type admitClients struct {
	identities *armmsi.UserAssignedIdentitiesClient
	fics       *armmsi.FederatedIdentityCredentialsClient
	roles      *armauthorization.RoleAssignmentsClient
}

func azureAdmitClients(subscriptionID string, credential azcore.TokenCredential) (*admitClients, error) {
	msiFactory, err := armmsi.NewClientFactory(subscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed creating managed identity client factory: %w", err)
	}
	roles, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed creating role assignments client: %w", err)
	}
	return &admitClients{
		identities: msiFactory.NewUserAssignedIdentitiesClient(),
		fics:       msiFactory.NewFederatedIdentityCredentialsClient(),
		roles:      roles,
	}, nil
}

func leaseCredential(request assets.LeaseRequest) (azcore.TokenCredential, string, error) {
	if request.AcquiredSlotState == nil {
		return nil, "", errors.New("acquired slot state is nil")
	}
	subscriptionID := strings.TrimSpace(request.AcquiredSlotState.Slot.Subscriptions.Infrastructure.ID)
	if subscriptionID == "" {
		return nil, "", errors.New("resolved infrastructure subscription ID is empty")
	}
	credential, err := azureutil.ClusterProfileCredential(request.SelectedClusterProfileDir)
	if err != nil {
		return nil, "", err
	}
	return credential, subscriptionID, nil
}

func leasedBundleName(request assets.LeaseRequest) (string, error) {
	if request.AcquiredSlotState == nil {
		return "", errors.New("resolved infrastructure identity inventory is empty")
	}
	asset := request.AcquiredSlotState.Slot.Assets.InfrastructureIdentities
	if asset == nil || len(asset.ResourceGroups) != 1 {
		return "", errors.New("resolved infrastructure identity inventory is empty")
	}
	return asset.ResourceGroups[0], nil
}

func admitIdentityLeaseWithClients(ctx context.Context, request assets.LeaseRequest, topology Topology, clients *admitClients, cleanupWorkers int) error {
	logger := logr.FromContextOrDiscard(ctx)
	leaseName, err := leasedBundleName(request)
	if err != nil {
		return err
	}
	groups, err := topology.AzureResourceGroups(leaseName)
	if err != nil {
		return err
	}
	logger.Info("Loading infrastructure identity lease inventory",
		"slotName", request.AcquiredSlotState.Slot.ResourceName,
		"bundle", leaseName,
	)
	principals := sets.New[uuid.UUID]()
	var federatedCredentials []azureutil.FederatedCredentialRef
	for _, group := range groups {
		identities, err := azureutil.InspectResourceGroup(ctx, clients.identities, group.Name, group.IdentityNames)
		if err != nil {
			return err
		}
		for _, identityName := range group.IdentityNames {
			actualName := identities[strings.ToLower(identityName)].Name
			refs, err := azureutil.ListFederatedCredentials(ctx, clients.fics, group.Name, actualName)
			if err != nil {
				return err
			}
			federatedCredentials = append(federatedCredentials, refs...)
		}
		for _, identity := range identities {
			principals.Insert(identity.PrincipalID)
		}
	}
	assignments, err := azureutil.RoleAssignmentsForPrincipals(ctx, clients.roles, principals)
	if err != nil {
		return err
	}
	logger.Info(fmt.Sprintf("Loaded infrastructure identity lease inventory with %d federated credentials and %d role assignments",
		len(federatedCredentials), len(assignments)))
	operations := azureutil.DeleteFederatedCredentialOps(logger, clients.fics, federatedCredentials)
	operations = append(operations, azureutil.DeleteRoleAssignmentOps(logger, clients.roles, assignments)...)
	workers := max(cleanupWorkers, 1)
	logger.Info("Deleting federated credentials and role assignments", "count", len(operations), "workers", workers)
	if err := azureutil.RunWithWorkers(ctx, operations, workers); err != nil {
		return fmt.Errorf("failed cleaning infrastructure identity lease: %w", err)
	}
	return nil
}
