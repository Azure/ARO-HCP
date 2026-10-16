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

package operationutils

import (
	"context"
	"errors"
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

var ErrCredentialIntentComplete = errors.New("credential intent already completed")

// LinkCredentialIntent recovers a crash between creating an internal document
// and linking its operation. Internal controllers call it before deletion, so an
// unlinked completed intent cannot disappear and subsequently be redispatched.
// Terminal ARM status does not prevent recovery or internal reconciliation.
func LinkCredentialIntent(ctx context.Context, db corecosmosstorage.ResourcesDBClient, id *azcorearm.ResourceID, operationID string, revocation bool) error {
	op, err := db.Operations(id.SubscriptionID).Get(ctx, operationID)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // The ARM retention window has elapsed.
	}
	if err != nil {
		return err
	}
	if op.ExternalID == nil || !strings.EqualFold(op.ExternalID.String(), id.Parent.String()) {
		return fmt.Errorf("credential intent does not belong to operation %s", operationID)
	}
	var linked *azcorearm.ResourceID
	if revocation {
		if op.Request != coreapi.OperationRequestSystemAdminCredentialRevocation {
			return fmt.Errorf("operation %s is not a revocation", operationID)
		}
		if op.SystemAdminCredentialRevocation == nil {
			op.SystemAdminCredentialRevocation = &coreapi.OperationSystemAdminCredentialRevocation{}
		}
		linked = op.SystemAdminCredentialRevocation.SystemAdminCredentialRevocationResourceID
		if op.SystemAdminCredentialRevocation.ReconciliationComplete {
			return ErrCredentialIntentComplete
		}
		op.SystemAdminCredentialRevocation.SystemAdminCredentialRevocationResourceID = id
	} else {
		if op.Request != coreapi.OperationRequestSystemAdminCredentialRequest {
			return fmt.Errorf("operation %s is not a credential request", operationID)
		}
		if op.SystemAdminCredentialRequest == nil {
			op.SystemAdminCredentialRequest = &coreapi.OperationSystemAdminCredentialRequest{}
		}
		linked = op.SystemAdminCredentialRequest.SystemAdminCredentialRequestResourceID
		if op.SystemAdminCredentialRequest.ReconciliationComplete {
			return ErrCredentialIntentComplete
		}
		op.SystemAdminCredentialRequest.SystemAdminCredentialRequestResourceID = id
	}
	if linked != nil {
		if !strings.EqualFold(linked.String(), id.String()) {
			return fmt.Errorf("operation %s already linked to another credential intent", operationID)
		}
		return nil
	}
	_, err = db.Operations(id.SubscriptionID).Replace(ctx, op, nil)
	return err
}

// CompleteCredentialIntent is the durable deletion acknowledgement. It changes
// only internal dispatch state, including when ARM monitoring already failed.
func CompleteCredentialIntent(ctx context.Context, db corecosmosstorage.ResourcesDBClient, id *azcorearm.ResourceID, operationID string, revocation bool) error {
	if err := LinkCredentialIntent(ctx, db, id, operationID, revocation); err != nil && !errors.Is(err, ErrCredentialIntentComplete) {
		return err
	}
	op, err := db.Operations(id.SubscriptionID).Get(ctx, operationID)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if revocation {
		if op.SystemAdminCredentialRevocation.ReconciliationComplete {
			return nil
		}
		op.SystemAdminCredentialRevocation.ReconciliationComplete = true
	} else {
		if op.SystemAdminCredentialRequest.ReconciliationComplete {
			return nil
		}
		op.SystemAdminCredentialRequest.ReconciliationComplete = true
	}
	_, err = db.Operations(id.SubscriptionID).Replace(ctx, op, nil)
	return err
}
