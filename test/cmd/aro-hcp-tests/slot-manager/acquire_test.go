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

package slotmanager

import (
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

func TestResolveLeasedSlot(t *testing.T) {
	t.Parallel()

	opts := &AcquireOptions{
		completedAcquireOptions: &completedAcquireOptions{
			PoolEnvironment: "dev",
			Pool: slots.Pool{
				SubscriptionName:        "dev-subscription",
				Region:                  "westus3",
				ResourceType:            "aro-hcp-dev-westus3-slot",
				SlotCount:               2,
				IdentityContainerPrefix: "aro-hcp-msi-container-dev",
				IdentityContainerCount:  1,
			},
		},
	}

	slot, err := opts.ResolveLeasedSlot("aro-hcp-dev-westus3-slot-01")
	if err != nil {
		t.Fatalf("expected leased slot resolution to succeed: %v", err)
	}
	if slot.SlotIndex != 1 {
		t.Fatalf("expected slot index 1, got %d", slot.SlotIndex)
	}
}

func TestResolveLeasedSlotRejectsResourceOutsideSelectedPool(t *testing.T) {
	t.Parallel()

	opts := &AcquireOptions{
		completedAcquireOptions: &completedAcquireOptions{
			PoolEnvironment: "dev",
			Pool: slots.Pool{
				SubscriptionName:        "dev-subscription",
				Region:                  "westus3",
				ResourceType:            "aro-hcp-dev-westus3-slot",
				SlotCount:               2,
				IdentityContainerPrefix: "aro-hcp-msi-container-dev",
				IdentityContainerCount:  1,
			},
		},
	}

	_, err := opts.ResolveLeasedSlot("aro-hcp-prod-uksouth-slot-00")
	if err == nil {
		t.Fatal("expected leased slot resolution to fail for a resource outside the selected pool")
	}
	if !strings.Contains(err.Error(), "not part of selected pool") {
		t.Fatalf("expected selected pool mismatch error, got %v", err)
	}
}
