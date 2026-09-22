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

package register

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func NewRegisterCommand() (*cobra.Command, error) {
	opts := DefaultRegisterOptions()
	cmd := &cobra.Command{
		Use:   "register",
		Short: "Register a stamp and management cluster in CosmosDB",
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), opts)
		},
	}
	if err := BindRegisterOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

func run(ctx context.Context, rawOpts *RawRegisterOptions) error {
	validated, err := rawOpts.Validate(ctx)
	if err != nil {
		return err
	}
	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}
	return completed.Run(ctx)
}

func (o *RegisterOptions) Run(ctx context.Context) error {
	if err := o.registerStamp(ctx); err != nil {
		return fmt.Errorf("stamp registration failed: %w", err)
	}

	if err := o.registerManagementCluster(ctx); err != nil {
		return fmt.Errorf("management cluster registration failed: %w", err)
	}

	return nil
}

func (o *RegisterOptions) registerStamp(ctx context.Context) error {
	logger := utils.LoggerFromContext(ctx).WithValues("stampIdentifier", o.stampIdentifier)
	stampsCRUD := o.fleetDBClient.Stamps()

	existing, err := stampsCRUD.Get(ctx, o.stampIdentifier)
	if err != nil {
		if !cosmosstorageutils.IsNotFoundError(err) {
			return fmt.Errorf("failed to get stamp %q: %w", o.stampIdentifier, err)
		}

		newStamp := &fleetapi.Stamp{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   o.stampResourceID,
				PartitionKey: strings.ToLower(o.stampIdentifier),
			},
		}
		o.applyAutoApprove(newStamp)

		logger.Info("Creating stamp", "autoApprove", o.autoApprove)
		if _, err := stampsCRUD.Create(ctx, newStamp, nil); err != nil {
			return fmt.Errorf("failed to create stamp %q: %w", o.stampIdentifier, err)
		}
		logger.Info("Stamp created")
		return nil
	}

	updated := existing.DeepCopy()
	o.applyAutoApprove(updated)

	logger.Info("Updating existing stamp", "autoApprove", o.autoApprove)
	if _, err := stampsCRUD.Replace(ctx, updated, existing, nil); err != nil {
		return fmt.Errorf("failed to update stamp %q: %w", o.stampIdentifier, err)
	}
	logger.Info("Stamp updated")
	return nil
}

func (o *RegisterOptions) applyAutoApprove(stamp *fleetapi.Stamp) {
	if !o.autoApprove {
		return
	}
	apimeta.SetStatusCondition(&stamp.Status.Conditions, metav1.Condition{
		Type:               string(fleetapi.StampConditionApproved),
		Status:             metav1.ConditionTrue,
		Reason:             string(fleetapi.StampConditionReasonAutoApproved),
		Message:            "Auto-approved during registration",
		LastTransitionTime: metav1.NewTime(time.Now()),
	})
}

func (o *RegisterOptions) registerManagementCluster(ctx context.Context) error {
	logger := utils.LoggerFromContext(ctx).WithValues("stampIdentifier", o.stampIdentifier)
	stampsCRUD := o.fleetDBClient.Stamps()

	if _, err := stampsCRUD.Get(ctx, o.stampIdentifier); err != nil {
		if cosmosstorageutils.IsNotFoundError(err) {
			return fmt.Errorf("parent stamp %q not found: register the stamp first", o.stampIdentifier)
		}
		return fmt.Errorf("failed to verify parent stamp %q: %w", o.stampIdentifier, err)
	}

	managementClusterCRUD := stampsCRUD.ManagementClusters(o.stampIdentifier)

	existing, err := managementClusterCRUD.Get(ctx, fleetapi.ManagementClusterResourceName)
	if err != nil {
		if !cosmosstorageutils.IsNotFoundError(err) {
			return fmt.Errorf("failed to get management cluster for stamp %q: %w", o.stampIdentifier, err)
		}

		managementCluster := &fleetapi.ManagementCluster{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   o.managementClusterResourceID,
				PartitionKey: strings.ToLower(o.stampIdentifier),
			},
		}
		o.applyToManagementCluster(managementCluster)

		logger.Info("Creating management cluster")
		if _, err := managementClusterCRUD.Create(ctx, managementCluster, nil); err != nil {
			return fmt.Errorf("failed to create management cluster for stamp %q: %w", o.stampIdentifier, err)
		}
		logger.Info("Management cluster created")
		return nil
	}

	// Existing documents keep immutable registration identity fields. Re-applying
	// current configuration to those fields fails Fleet update validation whenever
	// configuration has drifted (for example a DNS zone rename).
	updated := existing.DeepCopy()
	o.warnImmutableManagementClusterDrift(ctx, existing)
	o.applyMutableStatusToManagementCluster(updated)

	logger.Info("Updating existing management cluster")
	if _, err := managementClusterCRUD.Replace(ctx, updated, existing, nil); err != nil {
		return fmt.Errorf("failed to update management cluster for stamp %q: %w", o.stampIdentifier, err)
	}
	logger.Info("Management cluster updated")
	return nil
}

func (o *RegisterOptions) applyToManagementCluster(managementCluster *fleetapi.ManagementCluster) {
	managementCluster.Spec.SchedulingPolicy = o.schedulingPolicy
	o.applyImmutableStatusToManagementCluster(managementCluster)
	o.applyMutableStatusToManagementCluster(managementCluster)
}

// applyImmutableStatusToManagementCluster sets registration-owned status fields that
// ValidateManagementClusterUpdate marks immutable. Call only when creating a new
// ManagementCluster document.
func (o *RegisterOptions) applyImmutableStatusToManagementCluster(managementCluster *fleetapi.ManagementCluster) {
	managementCluster.Status.AKSResourceID = o.aksResourceID
	managementCluster.Status.PublicDNSZoneResourceID = o.publicDNSZoneResourceID
	managementCluster.Status.HostedClustersSecretsKeyVaultURL = o.hostedClustersSecretsKeyVaultURL
	managementCluster.Status.HostedClustersManagedIdentitiesKeyVaultURL = o.hostedClustersManagedIdentitiesKeyVaultURL
	managementCluster.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID = o.hostedClustersSecretsKeyVaultManagedIdentityClientID
	managementCluster.Status.MaestroConsumerName = o.maestroConsumerName
	managementCluster.Status.MaestroRESTAPIURL = o.maestroRESTAPIURL
	managementCluster.Status.MaestroGRPCTarget = o.maestroGRPCTarget
	managementCluster.Status.KubeApplierCosmosContainerName = o.kubeApplierCosmosContainerName
}

// applyMutableStatusToManagementCluster sets registration-owned status fields that
// may still change on update. Today that is only KubeApplierCosmosContainerName when
// the persisted value is empty (validator allows empty→set, then immutable).
// ClusterServiceProvisionShardID, Conditions, SharedIngressIPAddresses, and
// Spec.SchedulingPolicy are owned elsewhere and must not be written here.
func (o *RegisterOptions) applyMutableStatusToManagementCluster(managementCluster *fleetapi.ManagementCluster) {
	if managementCluster.Status.KubeApplierCosmosContainerName == "" {
		managementCluster.Status.KubeApplierCosmosContainerName = o.kubeApplierCosmosContainerName
	}
}

// warnImmutableManagementClusterDrift logs when configured RegisterOptions values
// differ from persisted immutable status fields. The persisted values are kept.
func (o *RegisterOptions) warnImmutableManagementClusterDrift(ctx context.Context, existing *fleetapi.ManagementCluster) {
	logger := utils.LoggerFromContext(ctx)
	warnIfDrift(logger, "status.aksResourceID", resourceIDString(existing.Status.AKSResourceID), resourceIDString(o.aksResourceID))
	warnIfDrift(logger, "status.publicDNSZoneResourceID", resourceIDString(existing.Status.PublicDNSZoneResourceID), resourceIDString(o.publicDNSZoneResourceID))
	warnIfDrift(logger, "status.hostedClustersSecretsKeyVaultURL", existing.Status.HostedClustersSecretsKeyVaultURL, o.hostedClustersSecretsKeyVaultURL)
	warnIfDrift(logger, "status.hostedClustersManagedIdentitiesKeyVaultURL", existing.Status.HostedClustersManagedIdentitiesKeyVaultURL, o.hostedClustersManagedIdentitiesKeyVaultURL)
	warnIfDrift(logger, "status.hostedClustersSecretsKeyVaultManagedIdentityClientID", existing.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID, o.hostedClustersSecretsKeyVaultManagedIdentityClientID)
	warnIfDrift(logger, "status.maestroConsumerName", existing.Status.MaestroConsumerName, o.maestroConsumerName)
	warnIfDrift(logger, "status.maestroRESTAPIURL", existing.Status.MaestroRESTAPIURL, o.maestroRESTAPIURL)
	warnIfDrift(logger, "status.maestroGRPCTarget", existing.Status.MaestroGRPCTarget, o.maestroGRPCTarget)
	if existing.Status.KubeApplierCosmosContainerName != "" {
		warnIfDrift(logger, "status.kubeApplierCosmosContainerName", existing.Status.KubeApplierCosmosContainerName, o.kubeApplierCosmosContainerName)
	}
}

func warnIfDrift(logger logr.Logger, field, persisted, configured string) {
	if persisted == configured {
		return
	}
	logger.Info("configured immutable field differs from persisted value; retaining persisted value",
		"field", field,
		"persisted", persisted,
		"configured", configured,
	)
}

func resourceIDString(id *azcorearm.ResourceID) string {
	if id == nil {
		return ""
	}
	return id.String()
}
