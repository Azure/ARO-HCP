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
	"time"

	"github.com/spf13/cobra"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/database"
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
		if !database.IsNotFoundError(err) {
			return fmt.Errorf("failed to get stamp %q: %w", o.stampIdentifier, err)
		}

		newStamp := &fleet.Stamp{
			CosmosMetadata: api.CosmosMetadata{ResourceID: o.stampResourceID},
			ResourceID:     o.stampResourceID,
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

func (o *RegisterOptions) applyAutoApprove(stamp *fleet.Stamp) {
	if !o.autoApprove {
		return
	}
	apimeta.SetStatusCondition(&stamp.Status.Conditions, metav1.Condition{
		Type:               string(fleet.StampConditionApproved),
		Status:             metav1.ConditionTrue,
		Reason:             string(fleet.StampConditionReasonAutoApproved),
		Message:            "Auto-approved during registration",
		LastTransitionTime: metav1.NewTime(time.Now()),
	})
}

func (o *RegisterOptions) registerManagementCluster(ctx context.Context) error {
	logger := utils.LoggerFromContext(ctx).WithValues("stampIdentifier", o.stampIdentifier)
	stampsCRUD := o.fleetDBClient.Stamps()

	if _, err := stampsCRUD.Get(ctx, o.stampIdentifier); err != nil {
		if database.IsNotFoundError(err) {
			return fmt.Errorf("parent stamp %q not found: register the stamp first", o.stampIdentifier)
		}
		return fmt.Errorf("failed to verify parent stamp %q: %w", o.stampIdentifier, err)
	}

	managementClusterCRUD := stampsCRUD.ManagementClusters(o.stampIdentifier)

	existing, err := managementClusterCRUD.Get(ctx, fleet.ManagementClusterResourceName)
	if err != nil {
		if !database.IsNotFoundError(err) {
			return fmt.Errorf("failed to get management cluster for stamp %q: %w", o.stampIdentifier, err)
		}

		managementCluster := &fleet.ManagementCluster{
			CosmosMetadata: api.CosmosMetadata{ResourceID: o.managementClusterResourceID},
			ResourceID:     o.managementClusterResourceID,
		}
		o.applyToManagementCluster(managementCluster)

		logger.Info("Creating management cluster")
		if _, err := managementClusterCRUD.Create(ctx, managementCluster, nil); err != nil {
			return fmt.Errorf("failed to create management cluster for stamp %q: %w", o.stampIdentifier, err)
		}
		logger.Info("Management cluster created")
		return nil
	}

	updated := existing.DeepCopy()
	o.applyToManagementCluster(updated)

	logger.Info("Updating existing management cluster")
	if _, err := managementClusterCRUD.Replace(ctx, updated, existing, nil); err != nil {
		return fmt.Errorf("failed to update management cluster for stamp %q: %w", o.stampIdentifier, err)
	}
	logger.Info("Management cluster updated")
	return nil
}

func (o *RegisterOptions) applyToManagementCluster(managementCluster *fleet.ManagementCluster) {
	managementCluster.Spec.SchedulingPolicy = o.schedulingPolicy
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
