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

package credentialrevocation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Owns checks the live cluster immediately before revocation side effects. This
// is optimistic exclusion, not fencing of writes to the management cluster.
func Owns(ctx context.Context, db corecosmosstorage.ResourcesDBClient, revocation *coreapi.SystemAdminCredentialRevocation) (bool, error) {
	id := revocation.ResourceID.Parent
	cluster, err := db.HCPClusters(id.SubscriptionID, id.ResourceGroupName).Get(ctx, id.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	owner := cluster.ServiceProviderProperties.SystemAdminCredentialRevocationResourceID
	return owner != nil && strings.EqualFold(owner.String(), revocation.ResourceID.String()) &&
		cluster.ServiceProviderProperties.DeletionTimestamp == nil, nil
}

type ownershipDecision struct {
	Cluster *coreapi.Cluster
	Owned   bool
	Err     error
}

func (d ownershipDecision) validate() {
	if (d.Cluster != nil && (d.Err != nil || d.Owned)) || (d.Owned && d.Err != nil) {
		panic("invalid credential ownership decision: mutation and error")
	}
}

// decideLegacyReference migrates only the legacy sentinel. It is not a repair
// mechanism for arbitrary dangling internal references. Teardown releases those.
func decideLegacyReference(logger logr.Logger, cluster *coreapi.Cluster, revocations []*coreapi.SystemAdminCredentialRevocation, listErr error, operation *coreapi.Operation, operationErr error) ownershipDecision {
	props := cluster.ServiceProviderProperties
	if props.SystemAdminCredentialRevocationResourceID != nil || props.RevokeCredentialsOperationID == "" {
		return ownershipDecision{}
	}
	if listErr != nil {
		return ownershipDecision{Err: listErr}
	}
	var owner *coreapi.SystemAdminCredentialRevocation
	for _, rev := range revocations {
		if rev.Spec.OperationID == props.RevokeCredentialsOperationID {
			owner = rev
		}
	}
	// An established internal owner takes precedence over a late legacy sentinel.
	for _, rev := range revocations {
		if meta.IsStatusConditionTrue(rev.Status.Conditions, coreapi.SystemAdminCredentialRevocationConditionActive) {
			owner = rev
			break
		}
	}
	if owner != nil {
		if owner.Status.DeletionTimestamp != nil || cluster.ServiceProviderProperties.DeletionTimestamp != nil {
			return ownershipDecision{}
		}
		replacement := cluster.DeepCopy()
		replacement.ServiceProviderProperties.SystemAdminCredentialRevocationResourceID = owner.ResourceID
		replacement.ServiceProviderProperties.RevokeCredentialsOperationID = owner.Spec.OperationID
		logger.Info("Attempting to migrate legacy revocation reference", "revocation", owner.ResourceID.String())
		return ownershipDecision{Cluster: replacement}
	}
	if operationErr != nil && !cosmosstorageutils.IsNotFoundError(operationErr) {
		return ownershipDecision{Err: operationErr}
	}
	if operation != nil && !operation.Status.IsTerminal() && (operation.Status == coreapi.ProvisioningStateAccepted || operation.SystemAdminCredentialRevocation == nil || operation.SystemAdminCredentialRevocation.SystemAdminCredentialRevocationResourceID == nil) {
		return ownershipDecision{} // DispatchRevokeCredentials has not finished dispatching its intent yet.
	}
	replacement := cluster.DeepCopy()
	replacement.ServiceProviderProperties.RevokeCredentialsOperationID = ""
	logger.Info("Attempting to release completed legacy revocation sentinel", "operation", props.RevokeCredentialsOperationID)
	return ownershipDecision{Cluster: replacement}
}

// MigrateLegacyReference is shared with CosmosMigration because it runs once per
// subscription, while old typed writers can subsequently erase the new field.
// Live observations plus the cluster ETag prevent a stale cache from releasing
// a newly established sentinel. They do not fence old backend side effects.
func MigrateLegacyReference(ctx context.Context, db corecosmosstorage.ResourcesDBClient, cluster *coreapi.Cluster) error {
	id := cluster.ID
	crud := db.HCPClusters(id.SubscriptionID, id.ResourceGroupName)
	var revocations []*coreapi.SystemAdminCredentialRevocation
	iter, listErr := crud.SystemAdminCredentialRevocations(id.Name).List(ctx, nil)
	if listErr == nil {
		for _, rev := range iter.Items(ctx) {
			revocations = append(revocations, rev)
		}
		listErr = iter.GetError()
	}
	var op *coreapi.Operation
	var opErr error
	if cluster.ServiceProviderProperties.RevokeCredentialsOperationID != "" {
		op, opErr = db.Operations(id.SubscriptionID).Get(ctx, cluster.ServiceProviderProperties.RevokeCredentialsOperationID)
	}
	d := decideLegacyReference(utils.LoggerFromContext(ctx), cluster, revocations, listErr, op, opErr)
	d.validate()
	if d.Cluster == nil {
		return d.Err
	}
	_, err := crud.Replace(ctx, d.Cluster, nil)
	return err
}

const SystemAdminCredentialRevocationLegacyReferenceControllerName = "SystemAdminCredentialRevocationLegacyReference"

type legacyReferenceController struct {
	db       corecosmosstorage.ResourcesDBClient
	clusters corelisters.ClusterLister
}

func NewLegacyReferenceController(db corecosmosstorage.ResourcesDBClient, informers coreinformers.BackendInformers) controllerutils.Controller {
	_, clusters := informers.Clusters()
	controller := controllerutils.NewClusterWatchingController(SystemAdminCredentialRevocationLegacyReferenceControllerName, db, informers, nil, time.Minute, &legacyReferenceController{db: db, clusters: clusters})
	queue := controller.(controllerutils.Enqueuer)
	onInput := func(obj any) {
		if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			obj = tombstone.Obj
		}
		switch input := obj.(type) {
		case *coreapi.SystemAdminCredentialRevocation:
			id := input.ResourceID.Parent
			queue.Enqueue(controllerutils.HCPClusterKey{SubscriptionID: id.SubscriptionID, ResourceGroupName: id.ResourceGroupName, HCPClusterName: id.Name})
		case *coreapi.Operation:
			if input.Request == coreapi.OperationRequestSystemAdminCredentialRevocation && input.ExternalID != nil {
				id := input.ExternalID
				queue.Enqueue(controllerutils.HCPClusterKey{SubscriptionID: id.SubscriptionID, ResourceGroupName: id.ResourceGroupName, HCPClusterName: id.Name})
			}
		}
	}
	revocations, _ := informers.SystemAdminCredentialRevocations()
	operations := informers.AllOperations()
	for _, informer := range []cache.SharedIndexInformer{revocations, operations} {
		if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: onInput, DeleteFunc: onInput, UpdateFunc: func(old, new any) { onInput(old); onInput(new) }}); err != nil {
			panic(err)
		}
	}
	return controller
}

func (c *legacyReferenceController) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	if _, err := c.clusters.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName); err != nil {
		if cosmosstorageutils.IsNotFoundError(err) {
			return nil
		}
		return err
	}
	cluster, err := c.db.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return MigrateLegacyReference(ctx, c.db, cluster)
}

// Claim uses one cluster ETag write. Active internal owners survive erasure of
// the new cluster field; pending documents do not block each other.
func Claim(ctx context.Context, db corecosmosstorage.ResourcesDBClient, rev *coreapi.SystemAdminCredentialRevocation) (bool, error) {
	if rev.Status.DeletionTimestamp != nil {
		return false, nil
	}
	id := rev.ResourceID.Parent
	crud := db.HCPClusters(id.SubscriptionID, id.ResourceGroupName)
	cluster, err := crud.Get(ctx, id.Name)
	if err != nil {
		return false, err
	}
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return false, nil
	}
	// Read the intent after the cluster: if teardown releases this observed
	// cluster ETag concurrently, the claim fails rather than reacquiring it.
	liveRev, err := crud.SystemAdminCredentialRevocations(id.Name).Get(ctx, rev.ResourceID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	iter, err := crud.SystemAdminCredentialRevocations(id.Name).List(ctx, nil)
	if err != nil {
		return false, err
	}
	var others []*coreapi.SystemAdminCredentialRevocation
	for _, other := range iter.Items(ctx) {
		others = append(others, other)
	}
	if err := iter.GetError(); err != nil {
		return false, err
	}
	d := decideClaim(utils.LoggerFromContext(ctx), cluster, liveRev, others)
	d.validate()
	if d.Cluster == nil {
		return d.Owned, d.Err
	}
	if _, err := crud.Replace(ctx, d.Cluster, nil); err != nil {
		return false, fmt.Errorf("claim revocation: %w", err)
	}
	return true, nil
}

func decideClaim(logger logr.Logger, cluster *coreapi.Cluster, rev *coreapi.SystemAdminCredentialRevocation, others []*coreapi.SystemAdminCredentialRevocation) ownershipDecision {
	if rev.Status.DeletionTimestamp != nil || cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return ownershipDecision{}
	}
	props := cluster.ServiceProviderProperties
	if owner := props.SystemAdminCredentialRevocationResourceID; owner != nil {
		return ownershipDecision{Owned: strings.EqualFold(owner.String(), rev.ResourceID.String())}
	}
	for _, other := range others {
		if !strings.EqualFold(other.ResourceID.String(), rev.ResourceID.String()) && meta.IsStatusConditionTrue(other.Status.Conditions, coreapi.SystemAdminCredentialRevocationConditionActive) {
			logger.V(2).Info("Waiting for active internal revocation", "owner", other.ResourceID.String())
			return ownershipDecision{}
		}
	}
	if props.RevokeCredentialsOperationID != "" && props.RevokeCredentialsOperationID != rev.Spec.OperationID {
		return ownershipDecision{}
	}
	replacement := cluster.DeepCopy()
	replacement.ServiceProviderProperties.SystemAdminCredentialRevocationResourceID = rev.ResourceID
	// Old frontend writers must continue to see exclusion even if they erase the
	// new field. Only an identity-matching teardown may remove this mirror.
	replacement.ServiceProviderProperties.RevokeCredentialsOperationID = rev.Spec.OperationID
	logger.Info("Attempting cluster revocation claim", "revocation", rev.ResourceID.String())
	return ownershipDecision{Cluster: replacement}
}
