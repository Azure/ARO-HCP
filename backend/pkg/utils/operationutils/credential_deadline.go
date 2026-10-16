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

	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
)

// ExpireCredentialOperation ends monitoring without canceling internal intent.
// Call before accessing cluster, placement, Cluster Service, or kube-applier state.
func ExpireCredentialOperation(ctx context.Context, clock utilsclock.PassiveClock, db corecosmosstorage.ResourcesDBClient, op *coreapi.Operation) (bool, error) {
	if op.Status.IsTerminal() || op.CompletionDeadline == nil || clock.Now().Before(*op.CompletionDeadline) {
		return false, nil
	}
	if op.Request != coreapi.OperationRequestSystemAdminCredentialRequest && op.Request != coreapi.OperationRequestSystemAdminCredentialRevocation {
		return false, nil
	}
	return true, UpdateOperationStatus(ctx, clock, db, op, coreapi.ProvisioningStateFailed, &coreapi.CloudErrorBody{
		Code:    "DeadlineExceeded",
		Message: "Credential operation monitoring deadline exceeded; dispatched internal work continues independently.",
	}, nil)
}
