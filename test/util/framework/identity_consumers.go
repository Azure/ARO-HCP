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
	"os"
	"strings"

	"github.com/go-logr/logr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	hcpsdk "github.com/Azure/ARO-HCP/test/sdk/v20261001preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

const IdentityConsumerGuardEnvvar = "ARO_HCP_IDENTITY_CONSUMER_GUARD"

// IdentityConsumerGuardMode validates the configured policy, defaulting to enforcement.
func IdentityConsumerGuardMode(mode string) (string, error) {
	if mode == "" {
		mode = "enforce"
	}
	if mode != "enforce" && mode != "audit" {
		return "", fmt.Errorf("%s must be enforce or audit, got %q", IdentityConsumerGuardEnvvar, mode)
	}
	return mode, nil
}

// CheckIdentityConsumers20261001 audits identity consumers and enforces safe reuse unless explicitly set to audit.
func CheckIdentityConsumers20261001(ctx context.Context, factory *hcpsdk.ClientFactory, subscriptionID string, containers []string, phase, mode string) error {
	mode, err := IdentityConsumerGuardMode(mode)
	if err != nil {
		return err
	}
	logger := logr.FromContextOrDiscard(ctx).WithValues(
		"phase", phase, "guardMode", mode, "subscriptionID", subscriptionID,
		"identityContainers", containers, "job", os.Getenv("JOB_NAME"), "buildID", os.Getenv("BUILD_ID"),
	)
	err = checkIdentityConsumers20261001(ctx, factory, subscriptionID, containers)
	if err != nil {
		var referenced *identityConsumerReferenceError
		if errors.As(err, &referenced) {
			logger = logger.WithValues("identity", referenced.identity, "consumer", referenced.consumer)
		}
		logger.Error(err, "Identity consumer guard could not establish safe cleanup and reuse", "blocked", mode == "enforce" || ctx.Err() != nil)
		if mode == "enforce" || ctx.Err() != nil {
			return err
		}
		return nil
	}
	logger.Info("Identity consumer inventory completed without matching references", "blocked", false)
	return nil
}

type identityConsumerReferenceError struct {
	identity string
	consumer string
}

// Error identifies the consumer preventing identity cleanup and reuse.
func (e *identityConsumerReferenceError) Error() string {
	return fmt.Sprintf("identity %q is still referenced by consumer %q", e.identity, e.consumer)
}

// Checks every HCP and node-pool page before declaring the inventory safe.
func checkIdentityConsumers20261001(ctx context.Context, factory *hcpsdk.ClientFactory, subscriptionID string, containers []string) error {
	if factory == nil || subscriptionID == "" || len(containers) == 0 {
		return errors.New("consumer check requires an HCP client, resolved subscription and identity containers")
	}
	leased := map[string]struct{}{}
	for _, group := range containers {
		for _, name := range NewDefaultIdentities().ToSlice() {
			id := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ManagedIdentity/userAssignedIdentities/%s", subscriptionID, group, name)
			leased[strings.ToLower(id)] = struct{}{}
		}
	}
	clusters := factory.NewHcpOpenShiftClustersClient().NewListBySubscriptionPager(nil)
	for clusters.More() {
		page, err := clusters.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed listing HCP consumers in subscription %q: %w", subscriptionID, err)
		}
		if page.Value == nil {
			return errors.New("HCP consumer list has no value array")
		}
		for _, cluster := range page.Value {
			if cluster == nil || cluster.ID == nil {
				return errors.New("HCP consumer list contains an entry without an ID")
			}
			id, err := azcorearm.ParseResourceID(*cluster.ID)
			if err != nil || !strings.EqualFold(id.SubscriptionID, subscriptionID) || !strings.EqualFold(id.ResourceType.String(), "Microsoft.RedHatOpenShift/hcpOpenShiftClusters") {
				return fmt.Errorf("HCP consumer list contains an invalid cluster ID %q", *cluster.ID)
			}
			if cluster.Properties == nil || cluster.Properties.Platform == nil ||
				cluster.Properties.Platform.OperatorsAuthentication == nil ||
				cluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities == nil {
				return fmt.Errorf("cannot inspect operator identities for HCP %q", *cluster.ID)
			}
			profile := cluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities
			if profile.ServiceManagedIdentity == nil || len(profile.ControlPlaneOperators) == 0 || len(profile.DataPlaneOperators) == 0 {
				return fmt.Errorf("incomplete operator identity profile for HCP %q", *cluster.ID)
			}
			references := []*string{profile.ServiceManagedIdentity}
			if registry := cluster.Properties.Platform.ContainerRegistry; registry != nil {
				references = append(references, registry.ManagedIdentity)
			}
			for _, identity := range profile.ControlPlaneOperators {
				references = append(references, identity)
			}
			for _, identity := range profile.DataPlaneOperators {
				references = append(references, identity)
			}
			if err := checkConsumerIdentities(*cluster.ID, cluster.Identity, references, leased); err != nil {
				return err
			}

			// All provisioning states count, including Failed and Deleting.
			nodePools := factory.NewNodePoolsClient().NewListByParentPager(id.ResourceGroupName, id.Name, nil)
			for nodePools.More() {
				page, err := nodePools.NextPage(ctx)
				if err != nil {
					return fmt.Errorf("failed listing node pool consumers for HCP %q: %w", *cluster.ID, err)
				}
				if page.Value == nil {
					return fmt.Errorf("node pool consumer list for HCP %q has no value array", *cluster.ID)
				}
				for _, nodePool := range page.Value {
					if nodePool == nil || nodePool.ID == nil {
						return fmt.Errorf("node pool consumer list for HCP %q contains an entry without an ID", *cluster.ID)
					}
					nodeID, err := azcorearm.ParseResourceID(*nodePool.ID)
					if err != nil || nodeID.Parent == nil || !strings.EqualFold(nodeID.Parent.String(), id.String()) ||
						!strings.EqualFold(nodeID.ResourceType.String(), "Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools") {
						return fmt.Errorf("invalid node pool consumer ID %q for HCP %q", *nodePool.ID, *cluster.ID)
					}
					if err := checkConsumerIdentities(*nodePool.ID, nodePool.Identity, nil, leased); err != nil {
						return err
					}
				}
			}
		}
	}
	return ctx.Err()
}

// Validates explicit identity references and reports any consumer of the leased inventory.
func checkConsumerIdentities(consumer string, identity *hcpsdk.ManagedServiceIdentity, references []*string, leased map[string]struct{}) error {
	if identity != nil {
		if identity.Type == nil {
			return fmt.Errorf("incomplete managed identity profile for consumer %q", consumer)
		}
		switch *identity.Type {
		case hcpsdk.ManagedServiceIdentityTypeNone, hcpsdk.ManagedServiceIdentityTypeSystemAssigned:
		case hcpsdk.ManagedServiceIdentityTypeUserAssigned, hcpsdk.ManagedServiceIdentityTypeSystemAssignedUserAssigned:
			if len(identity.UserAssignedIdentities) == 0 {
				return fmt.Errorf("incomplete managed identity profile for consumer %q", consumer)
			}
		default:
			return fmt.Errorf("unknown managed identity type %q for consumer %q", *identity.Type, consumer)
		}
		for reference := range identity.UserAssignedIdentities {
			references = append(references, &reference)
		}
	}
	for _, reference := range references {
		if reference == nil || *reference == "" || strings.TrimSpace(*reference) != *reference {
			return fmt.Errorf("consumer %q has an empty or malformed identity reference", consumer)
		}
		id, err := azcorearm.ParseResourceID(*reference)
		if err != nil || id.SubscriptionID == "" || id.ResourceGroupName == "" || !strings.EqualFold(id.ResourceType.String(), "Microsoft.ManagedIdentity/userAssignedIdentities") {
			return fmt.Errorf("consumer %q has an invalid managed identity ID %q", consumer, *reference)
		}
		if _, found := leased[strings.ToLower(id.String())]; found {
			return &identityConsumerReferenceError{identity: *reference, consumer: consumer}
		}
	}
	return nil
}
