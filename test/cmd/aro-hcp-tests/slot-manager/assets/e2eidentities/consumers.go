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

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	hcpsdk "github.com/Azure/ARO-HCP/test/sdk/v20261001preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

// Selects consumer-free containers without changing the slot's dedicated ownership inventory.
func selectIdentityLeaseContainers(ctx context.Context, request assets.LeaseRequest, factory *hcpsdk.ClientFactory) ([]string, error) {
	if request.AcquiredSlotState == nil {
		return nil, errors.New("acquired slot state is nil")
	}
	slot := request.AcquiredSlotState.Slot
	if slot.Environment == "dev" {
		logr.FromContextOrDiscard(ctx).Info("ARM consumer check does not cover DEV local frontend clusters", "phase", "acquisition")
		return slot.IdentityContainerNames(), ctx.Err()
	}
	available, err := framework.FilterIdentityConsumers20261001(ctx, factory, slot.Subscriptions.E2E.ID, slot.IdentityContainerNames(), "acquisition", request.IdentityConsumerGuardMode)
	if err != nil {
		return nil, err
	}
	minimum := max(1, request.MinimumIdentityContainers)
	if len(available) < minimum {
		return nil, fmt.Errorf("not enough safe identity containers: need %d, available %d of %d", minimum, len(available), len(slot.IdentityContainerNames()))
	}
	return available, nil
}
