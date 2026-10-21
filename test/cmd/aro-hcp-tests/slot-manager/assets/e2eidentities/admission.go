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

package e2eidentities

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
	"github.com/Azure/ARO-HCP/test/util/framework"
)

type admitClients struct {
	identities *armmsi.UserAssignedIdentitiesClient
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
		roles:      roles,
	}, nil
}

func leaseCredential(request assets.LeaseRequest) (azcore.TokenCredential, string, error) {
	if request.AcquiredSlotState == nil {
		return nil, "", errors.New("acquired slot state is nil")
	}
	subscriptionID := strings.TrimSpace(request.AcquiredSlotState.Slot.Subscriptions.E2E.ID)
	if subscriptionID == "" {
		return nil, "", errors.New("resolved E2E subscription ID is empty")
	}
	credential, err := azureutil.ClusterProfileCredential(request.SelectedClusterProfileDir)
	if err != nil {
		return nil, "", err
	}
	return credential, subscriptionID, nil
}

func admitIdentityLeaseWithClients(ctx context.Context, request assets.LeaseRequest, clients *admitClients, cleanupWorkers int) error {
	logger := logr.FromContextOrDiscard(ctx)
	if request.AcquiredSlotState == nil || len(request.AcquiredSlotState.Slot.IdentityContainerNames()) == 0 {
		return errors.New("resolved E2E identity inventory is empty")
	}
	groups := request.AcquiredSlotState.Slot.IdentityContainerNames()
	logger.Info("Loading identity lease inventory", "slotName", request.AcquiredSlotState.Slot.ResourceName, "resourceGroupCount", len(groups))
	expectedNames := framework.NewDefaultIdentities().ToSlice()
	principals := sets.New[uuid.UUID]()
	for _, resourceGroup := range groups {
		identities, err := azureutil.InspectResourceGroup(ctx, clients.identities, resourceGroup, expectedNames)
		if err != nil {
			return err
		}
		for _, identity := range identities {
			principals.Insert(identity.PrincipalID)
		}
	}
	assignments, err := azureutil.RoleAssignmentsForPrincipals(ctx, clients.roles, principals)
	if err != nil {
		return err
	}
	logger.Info(fmt.Sprintf("Loaded identity lease inventory with %d role assignments", len(assignments)))
	workers := max(cleanupWorkers, 1)
	logger.Info("Deleting role assignments", "count", len(assignments), "workers", workers)
	if err := azureutil.RunWithWorkers(ctx, azureutil.DeleteRoleAssignmentOps(logger, clients.roles, assignments), workers); err != nil {
		return fmt.Errorf("failed cleaning E2E identity lease: %w", err)
	}
	return nil
}
