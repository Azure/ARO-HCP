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

package systemadmincredentialcontrollers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/operationcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
)

// operationRequestCredentialDispatch is controller #1. On a fresh
// request-credential operation it creates a SystemAdminCredential
// Cosmos document (with a server-generated keypair, username, and
// 24h expiration) and stamps Operation.InternalID with the credential's
// resource ID.
//
// It does NOT create ApplyDesires or ReadDesires — that work is owned
// by the credential-informer-driven SystemAdminCredentialDesiresCreator
// controller. Splitting these jobs lets the dispatch path stay short
// (a single Cosmos write) and lets the desires path retry independently
// against the kube-applier container.
type operationRequestCredentialDispatch struct {
	clock              utilsclock.PassiveClock
	clusterLister      listers.ClusterLister
	resourcesDBClient  database.ResourcesDBClient
	notificationClient *http.Client
}

func NewOperationRequestCredentialDispatchController(
	clock utilsclock.PassiveClock,
	clusterLister listers.ClusterLister,
	resourcesDBClient database.ResourcesDBClient,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &operationRequestCredentialDispatch{
		clock:              clock,
		clusterLister:      clusterLister,
		resourcesDBClient:  resourcesDBClient,
		notificationClient: notificationClient,
	}
	return operationcontrollers.NewGenericOperationController(
		"SystemAdminCredentialRequestDispatch",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)
}

func (c *operationRequestCredentialDispatch) ShouldProcess(ctx context.Context, op *api.Operation) bool {
	if op.Status.IsTerminal() {
		return false
	}
	if op.Request != database.OperationRequestRequestCredential {
		return false
	}
	if len(op.InternalID.String()) > 0 {
		return false
	}
	if op.ExternalID == nil {
		return false
	}
	return true
}

func (c *operationRequestCredentialDispatch) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	op, err := c.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get operation: %w", err)
	}
	if !c.ShouldProcess(ctx, op) {
		return nil
	}

	clusterRID := op.ExternalID
	if _, err := c.clusterLister.Get(ctx, clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name); err != nil {
		if database.IsNotFoundError(err) {
			return c.cancelOperation(ctx, op, "Cluster no longer exists")
		}
		return fmt.Errorf("get cluster: %w", err)
	}

	credentialsCRUD := c.resourcesDBClient.HCPClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName).SystemAdminCredentials(clusterRID.Name)
	existing, err := findCredentialForOperation(ctx, credentialsCRUD, op.OperationID.Name)
	if err != nil {
		return fmt.Errorf("scan credentials for idempotency: %w", err)
	}
	if existing != nil {
		return c.recordDispatched(ctx, op, existing)
	}

	pubPEM, privPEM, err := systemadmincredential.GenerateKeypair()
	if err != nil {
		return fmt.Errorf("generate keypair: %w", err)
	}
	credName := systemadmincredential.NewCredentialName()
	credRID, err := api.ToSystemAdminCredentialResourceID(clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name, credName)
	if err != nil {
		return fmt.Errorf("derive credential resource ID: %w", err)
	}
	now := metav1.NewTime(c.clock.Now())
	credential := &api.SystemAdminCredential{
		CosmosMetadata: api.CosmosMetadata{ResourceID: credRID, PartitionKey: strings.ToLower(credRID.SubscriptionID)},
		Spec: api.SystemAdminCredentialSpec{
			Username:            defaultUsername,
			ExpirationTimestamp: metav1.NewTime(now.Add(24 * time.Hour)),
			OperationID:         op.OperationID.Name,
			PublicKeyPEM:        string(pubPEM),
			PrivateKeyPEM:       string(privPEM),
		},
		Status: api.SystemAdminCredentialStatus{
			Phase: api.SystemAdminCredentialPhaseRequested,
		},
	}
	if _, err := credentialsCRUD.Create(ctx, credential, nil); err != nil {
		return fmt.Errorf("create SystemAdminCredential: %w", err)
	}
	return c.recordDispatched(ctx, op, credential)
}

func (c *operationRequestCredentialDispatch) recordDispatched(ctx context.Context, op *api.Operation, credential *api.SystemAdminCredential) error {
	credRIDStr := credential.GetResourceID().String()
	internalID, err := api.NewInternalID(credRIDStr)
	if err != nil {
		return fmt.Errorf("derive InternalID from credential RID %q: %w", credRIDStr, err)
	}
	op.InternalID = internalID
	if _, err := c.resourcesDBClient.Operations(op.OperationID.SubscriptionID).Replace(ctx, op, nil); err != nil {
		return fmt.Errorf("update operation InternalID: %w", err)
	}
	return nil
}

func (c *operationRequestCredentialDispatch) cancelOperation(ctx context.Context, op *api.Operation, msg string) error {
	apihelpers.CancelOperation(op, c.clock.Now())
	op.Error.Message = msg
	if _, err := c.resourcesDBClient.Operations(op.OperationID.SubscriptionID).Replace(ctx, op, nil); err != nil {
		return err
	}
	return nil
}

// findCredentialForOperation walks the credentials under a cluster and
// returns the first one whose Spec.OperationID matches. The number of
// in-flight credentials per cluster is tiny (typically 0 or 1), so a
// linear scan is fine. Read from the DB (not lister) so just-created
// docs are picked up by an immediate retry.
func findCredentialForOperation(ctx context.Context, credentialsCRUD database.ResourceCRUD[api.SystemAdminCredential, *api.SystemAdminCredential], operationID string) (*api.SystemAdminCredential, error) {
	iter, err := credentialsCRUD.List(ctx, nil)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	for _, cred := range iter.Items(ctx) {
		if cred != nil && cred.Spec.OperationID == operationID {
			return cred, nil
		}
	}
	return nil, iter.GetError()
}
