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
	"strings"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
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
	Err     error
}

func (d ownershipDecision) validate() {
	if d.Cluster != nil && (d.Err != nil || d.Cluster.CosmosETag == "" || d.Cluster.ID == nil) {
		panic("invalid credential ownership decision: replacement requires identity, ETag and no error")
	}
}

// decideLegacyReference normalizes persisted legacy state without restarting its grace.
func decideLegacyReference(logger logr.Logger, cluster *coreapi.Cluster, revocations []*coreapi.SystemAdminCredentialRevocation, listErr error, operation *coreapi.Operation, operationErr error) ownershipDecision {
	props := cluster.ServiceProviderProperties
	if props.SystemAdminCredentialRevocationResourceID != nil && (props.SystemAdminCredentialRevocationRequestedAt != nil || props.RevokeCredentialsOperationID == "") {
		return ownershipDecision{}
	}
	if listErr != nil {
		return ownershipDecision{Err: listErr}
	}
	var owner *coreapi.SystemAdminCredentialRevocation
	for _, rev := range revocations {
		if (props.SystemAdminCredentialRevocationResourceID != nil && strings.EqualFold(rev.ResourceID.String(), props.SystemAdminCredentialRevocationResourceID.String())) || (props.SystemAdminCredentialRevocationResourceID == nil && rev.Spec.OperationID == props.RevokeCredentialsOperationID) {
			owner = rev
		}
	}
	// An established internal owner takes precedence over a late legacy sentinel.
	for _, rev := range revocations {
		if props.SystemAdminCredentialRevocationResourceID == nil && meta.IsStatusConditionTrue(rev.Status.Conditions, coreapi.SystemAdminCredentialRevocationConditionActive) {
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
		if operation != nil && owner.Spec.OperationID == props.RevokeCredentialsOperationID && !operation.StartTime.IsZero() {
			stamp := metav1.NewTime(operation.StartTime)
			replacement.ServiceProviderProperties.SystemAdminCredentialRevocationRequestedAt = &stamp
		} else if props.SystemAdminCredentialRevocationResourceID != nil {
			return ownershipDecision{} // Live work is authoritative; do not churn unchanged legacy state.
		}
		logger.Info("Attempting to migrate legacy revocation reference", "revocation", owner.ResourceID.String())
		return ownershipDecision{Cluster: replacement}
	}
	if props.RevokeCredentialsOperationID == "" {
		return ownershipDecision{}
	}
	if operationErr != nil && !cosmosstorageutils.IsNotFoundError(operationErr) {
		return ownershipDecision{Err: operationErr}
	}
	if operation != nil && !operation.StartTime.IsZero() {
		id, err := coreapihelpers.SystemAdminCredentialRevocationID(cluster.ID, props.RevokeCredentialsOperationID)
		if err != nil {
			return ownershipDecision{Err: err}
		}
		if operation.SystemAdminCredentialRevocation != nil && operation.SystemAdminCredentialRevocation.SystemAdminCredentialRevocationResourceID != nil {
			id = operation.SystemAdminCredentialRevocation.SystemAdminCredentialRevocationResourceID
		}
		if props.SystemAdminCredentialRevocationResourceID != nil && !strings.EqualFold(props.SystemAdminCredentialRevocationResourceID.String(), id.String()) {
			return ownershipDecision{}
		}
		replacement := cluster.DeepCopy()
		replacement.ServiceProviderProperties.SystemAdminCredentialRevocationResourceID = id
		stamp := metav1.NewTime(operation.StartTime)
		replacement.ServiceProviderProperties.SystemAdminCredentialRevocationRequestedAt = &stamp
		return ownershipDecision{Cluster: replacement}
	}
	if props.SystemAdminCredentialRevocationResourceID != nil || (operation != nil && !operation.Status.IsTerminal()) {
		return ownershipDecision{} // No original time: fail safe, never invent a new grace.
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
	props := cluster.ServiceProviderProperties
	if props.SystemAdminCredentialRevocationResourceID != nil && (props.SystemAdminCredentialRevocationRequestedAt != nil || props.RevokeCredentialsOperationID == "") {
		return nil
	}
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
