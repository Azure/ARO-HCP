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

package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/clusterresources"
	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// clusterDeleteOperationStatusInput shares the Cluster document across all delete
// checks in a reconciliation. The delete controller already has the Cluster
// document in hand when it builds the timeout message, so it passes it as the
// shared input instead of having each check re-fetch it from a lister.
type clusterDeleteOperationStatusInput struct {
	// Cluster is the Cluster document being deleted. It is always set by the
	// controller before the checks are evaluated.
	Cluster *coreapi.Cluster
}

// clusterDeleteClusterServiceDeletionCheck reports whether the Cluster Service
// deletion has been dispatched and whether the Cluster Service cluster is gone.
type clusterDeleteClusterServiceDeletionCheck struct{}

var _ operationbase.OperationStatusCalculator[clusterDeleteOperationStatusInput] = (*clusterDeleteClusterServiceDeletionCheck)(nil)

func (c *clusterDeleteClusterServiceDeletionCheck) GetSourceName() string {
	return "clusterServiceDeletion"
}

// TODO scale this out better. We likely want something we can aggregate, e.g. a UserFacingDeleteProgress condition.
func (c *clusterDeleteClusterServiceDeletionCheck) CalculateOperationStatus(_ context.Context, _ *coreapi.Operation, input clusterDeleteOperationStatusInput) (*operationbase.OperationState, error) {
	cluster := input.Cluster
	if cluster.ServiceProviderProperties.ClusterServiceID == nil {
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
	}
	if cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp == nil {
		return operationbase.NewOperationState(coreapi.ProvisioningStateDeleting, "ClusterService deletion not yet dispatched"), nil
	}
	return operationbase.NewOperationState(coreapi.ProvisioningStateDeleting, fmt.Sprintf("ClusterService cluster %s still exists (deletion dispatched at %s)",
		cluster.ServiceProviderProperties.ClusterServiceID,
		cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp.Format(time.RFC3339))), nil
}

// clusterDeleteClusterServiceStatusCheck reports the live Cluster Service state
// for a cluster that is still being torn down.
type clusterDeleteClusterServiceStatusCheck struct {
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ operationbase.OperationStatusCalculator[clusterDeleteOperationStatusInput] = (*clusterDeleteClusterServiceStatusCheck)(nil)

func (c *clusterDeleteClusterServiceStatusCheck) GetSourceName() string {
	return "clusterServiceStatus"
}

// TODO scale this out better. We likely want something we can aggregate, e.g. a UserFacingDeleteProgress condition.
func (c *clusterDeleteClusterServiceStatusCheck) CalculateOperationStatus(ctx context.Context, _ *coreapi.Operation, input clusterDeleteOperationStatusInput) (*operationbase.OperationState, error) {
	cluster := input.Cluster
	if cluster.ServiceProviderProperties.ClusterServiceID == nil {
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
	}

	csCtx, cancel := context.WithTimeout(ctx, controllerutils.CSReadTimeout)
	defer cancel()

	csClusterStatus, err := c.clusterServiceClient.GetClusterStatus(csCtx, *cluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		var ocmError *ocmerrors.Error
		if errors.As(err, &ocmError) && ocmError.Status() == http.StatusNotFound {
			return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
		}
		return nil, fmt.Errorf("failed to get ClusterService status: %w", err)
	}

	return operationbase.NewOperationState(coreapi.ProvisioningStateDeleting, fmt.Sprintf("ClusterService state is %q", csClusterStatus.State())), nil
}

// clusterDeleteDescendantResourcesCheck reports any descendant resources that are
// still present in Cosmos (both the core inventory and the kube-applier inventory
// on the management cluster).
type clusterDeleteDescendantResourcesCheck struct {
	resourcesDBClient    corecosmosstorage.ResourcesDBClient
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients
}

var _ operationbase.OperationStatusCalculator[clusterDeleteOperationStatusInput] = (*clusterDeleteDescendantResourcesCheck)(nil)

func (c *clusterDeleteDescendantResourcesCheck) GetSourceName() string {
	return "descendantResources"
}

func (c *clusterDeleteDescendantResourcesCheck) CalculateOperationStatus(ctx context.Context, _ *coreapi.Operation, input clusterDeleteOperationStatusInput) (*operationbase.OperationState, error) {
	cluster := input.Cluster
	typeCounts := map[string]int{}

	resourcesCRUD, err := c.resourcesDBClient.UntypedCRUD(*cluster.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to create untyped CRUD: %w", err)
	}
	if err := countDescendants(ctx, resourcesCRUD, shouldCountChild, typeCounts); err != nil {
		return nil, err
	}

	spc, err := c.resourcesDBClient.ServiceProviderClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return nil, utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster for kube-applier resource count: %w", err))
	}
	if spc != nil && spc.Status.ManagementClusterResourceID != nil {
		kaClient, err := c.kubeApplierDBClients.For(ctx, spc.Status.ManagementClusterResourceID)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to resolve kube-applier client: %w", err))
		}
		if kaClient == nil {
			return nil, utils.TrackError(fmt.Errorf("kube-applier inventory unavailable for management cluster %s", spc.Status.ManagementClusterResourceID))
		}
		kaCRUD, kaErr := kaClient.UntypedCRUD(*cluster.ID)
		if kaErr != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to create kube-applier untyped CRUD: %w", kaErr))
		} else if err := countDescendants(ctx, kaCRUD, shouldCountChild, typeCounts); err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to count kube-applier descendant resources: %w", err))
		}
	}

	if len(typeCounts) == 0 {
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
	}

	var parts []string
	for resourceType, count := range typeCounts {
		parts = append(parts, fmt.Sprintf("%d %s", count, resourceType))
	}
	slices.Sort(parts)
	return operationbase.NewOperationState(coreapi.ProvisioningStateDeleting, fmt.Sprintf("remaining resources: %s", strings.Join(parts, ", "))), nil
}

func shouldCountChild(child *cosmosstorageutils.TypedDocument) bool {
	lowered := strings.ToLower(child.ResourceType)
	if strings.Contains(lowered, strings.ToLower(coreapi.ControllerResourceTypeName)) {
		return false
	}
	if strings.Contains(lowered, strings.ToLower(coreapi.ManagementClusterContentResourceTypeName)) {
		return false
	}
	if strings.Contains(lowered, strings.ToLower(kubeapplierapi.ReadDesireResourceTypeName)) {
		return false
	}
	if strings.Contains(lowered, strings.ToLower(kubeapplierapi.ApplyDesireResourceTypeName)) {
		var partial struct {
			Spec struct {
				Type kubeapplierapi.ApplyDesireType `json:"type"`
			} `json:"spec"`
		}
		if json.Unmarshal(child.Properties, &partial) == nil && partial.Spec.Type == kubeapplierapi.ApplyDesireTypeDelete {
			return false
		}
	}
	return true
}

func countDescendants(ctx context.Context, crud cosmosstorageutils.UntypedResourceCRUD, shouldCount func(*cosmosstorageutils.TypedDocument) bool, typeCounts map[string]int) error {
	childIterator, err := crud.ListRecursive(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to list descendant resources: %w", err)
	}
	for _, child := range childIterator.Items(ctx) {
		if !shouldCount(child) {
			continue
		}
		typeCounts[child.ResourceType]++
	}
	if err := childIterator.GetError(); err != nil {
		return fmt.Errorf("error iterating descendant resources: %w", err)
	}
	return nil
}

// clusterDeleteHostedClusterCheck reports whether the mirrored HostedCluster is
// still present for the cluster being deleted.
type clusterDeleteHostedClusterCheck struct {
	readDesireLister kubeapplierlisters.ReadDesireLister
}

var _ operationbase.OperationStatusCalculator[clusterDeleteOperationStatusInput] = (*clusterDeleteHostedClusterCheck)(nil)

func (c *clusterDeleteHostedClusterCheck) GetSourceName() string {
	return "hostedCluster"
}

func (c *clusterDeleteHostedClusterCheck) CalculateOperationStatus(ctx context.Context, _ *coreapi.Operation, input clusterDeleteOperationStatusInput) (*operationbase.OperationState, error) {
	cluster := input.Cluster
	hostedCluster, _, err := kubeapplierhelpers.GetCachedHostedClusterForCluster(ctx, c.readDesireLister, cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get cached HostedCluster: %w", err)
	}
	if hostedCluster == nil {
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
	}
	return operationbase.NewOperationState(coreapi.ProvisioningStateDeleting, "HostedCluster still exists"), nil
}

// clusterDeleteResourceTeardownCheck reports the progress of the ApplyDesire
// teardown chain. It describes which step the deletion is waiting on, similar to
// the log output from ClusterResourcesController but formatted for user-facing
// operation messages.
type clusterDeleteResourceTeardownCheck struct {
	resourcesDBClient corecosmosstorage.ResourcesDBClient
	applyDesireLister kubeapplierlisters.ApplyDesireLister
}

var _ operationbase.OperationStatusCalculator[clusterDeleteOperationStatusInput] = (*clusterDeleteResourceTeardownCheck)(nil)

func (c *clusterDeleteResourceTeardownCheck) GetSourceName() string {
	return "resourceTeardown"
}

func (c *clusterDeleteResourceTeardownCheck) CalculateOperationStatus(ctx context.Context, _ *coreapi.Operation, input clusterDeleteOperationStatusInput) (*operationbase.OperationState, error) {
	cluster := input.Cluster
	spc, err := c.resourcesDBClient.ServiceProviderClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get ServiceProviderCluster: %w", err)
	}

	if spc.Status.ManagementClusterResourceID == nil {
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
	}

	applyDesires, err := c.applyDesireLister.ListForCluster(ctx, cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to list ApplyDesires: %w", err)
	}

	// Filter to desires owned by ClusterResourcesController
	var ownedDesires []*kubeapplierapi.ApplyDesire
	for _, desire := range applyDesires {
		if desire.Tags != nil && desire.Tags[kubeapplierapi.TagControllerName] == clusterresources.ClusterResourcesControllerName {
			ownedDesires = append(ownedDesires, desire)
		}
	}

	if len(ownedDesires) == 0 {
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
	}

	// Group by deletion step based on desire names. This mirrors the logic in
	// apply_desire_removal_steps.go but uses human-readable names instead of
	// exposing internal implementation details.
	stepCounts := make(map[string][]string)
	for _, desire := range ownedDesires {
		desireName := strings.ToLower(desire.ResourceID.Name)
		var stepName string
		switch {
		case desireName == strings.ToLower(clusterresources.DesireNameHostedCluster):
			stepName = "hosted cluster"
		case desireName == strings.ToLower(clusterresources.DesireNameNodePool):
			stepName = "node pools"
		case desireName == strings.ToLower(clusterresources.DesireNameManagedCluster):
			stepName = "managed cluster"
		case desireName == strings.ToLower(clusterresources.DesireNameHostedClusterNamespace) ||
			desireName == strings.ToLower(clusterresources.DesireNameControlPlaneNamespace):
			stepName = "namespaces"
		case desireName == strings.ToLower(clusterresources.DesireNamePodNetworkInstance):
			stepName = "pod network instances"
		case desireName == strings.ToLower(clusterresources.DesireNamePodNetwork):
			stepName = "pod networks"
		default:
			stepName = "cluster resources"
		}
		stepCounts[stepName] = append(stepCounts[stepName], desireName)
	}

	if len(stepCounts) == 0 {
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
	}

	// Build a human-readable message about what's being deleted.
	// Order by typical deletion sequence: resources -> nodepools -> hostedcluster -> network -> namespaces
	stepOrder := []string{
		"cluster resources",
		"node pools",
		"managed cluster",
		"hosted cluster",
		"pod network instances",
		"pod networks",
		"namespaces",
	}
	var messageParts []string
	for _, step := range stepOrder {
		if resources, ok := stepCounts[step]; ok {
			count := len(resources)
			messageParts = append(messageParts, fmt.Sprintf("%s (%d)", step, count))
		}
	}

	if len(messageParts) == 0 {
		return operationbase.NewOperationState(coreapi.ProvisioningStateDeleting, "cleaning up cluster resources"), nil
	}

	return operationbase.NewOperationState(coreapi.ProvisioningStateDeleting,
		fmt.Sprintf("tearing down %s", strings.Join(messageParts, ", "))), nil
}
