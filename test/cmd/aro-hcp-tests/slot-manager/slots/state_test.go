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

package slots

import (
	"os"
	"strings"
	"testing"
)

func TestWriteAndLoadAcquiredSlotStateAndEnvFile(t *testing.T) {
	t.Parallel()

	sharedDir := t.TempDir()
	state := &AcquiredSlotState{
		Version:           acquiredSlotStateVersion,
		DeployEnvironment: "ci01",
		RuntimeRegion:     "eastus2",
		Slot: ExpandedSlot{
			Environment:             "dev",
			SubscriptionName:        "dev",
			Region:                  "westus3",
			ResourceType:            "aro-hcp-dev-westus3-slot",
			ResourceName:            "aro-hcp-dev-westus3-slot-00",
			IdentityContainerPrefix: "aro-hcp-msi-container-dev-00",
			IdentityContainerCount:  3,
		},
		LeasedResourceName: "aro-hcp-dev-westus3-slot-00",
	}

	if err := WriteAcquiredSlotState(sharedDir, state); err != nil {
		t.Fatalf("expected state write to succeed: %v", err)
	}
	if err := WriteEnvFile(sharedDir, state, "ARO HCP E2E Hosted Clusters (EA Subscription)"); err != nil {
		t.Fatalf("expected env file write to succeed: %v", err)
	}

	loadedState, err := LoadAcquiredSlotState(sharedDir)
	if err != nil {
		t.Fatalf("expected state load to succeed: %v", err)
	}
	if loadedState.Slot.ResourceName != state.Slot.ResourceName {
		t.Fatalf("expected resource name %q, got %q", state.Slot.ResourceName, loadedState.Slot.ResourceName)
	}
	if loadedState.RuntimeRegion != state.RuntimeRegion {
		t.Fatalf("expected runtime region %q, got %q", state.RuntimeRegion, loadedState.RuntimeRegion)
	}

	envFile, err := EnvFile(sharedDir)
	if err != nil {
		t.Fatalf("expected env file path to resolve: %v", err)
	}
	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("expected env file read to succeed: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, `export ARO_HCP_E2E_SLOT_NAME='aro-hcp-dev-westus3-slot-00'`) {
		t.Fatalf("expected env file to contain ARO_HCP_E2E_SLOT_NAME export, got %q", content)
	}
	if !strings.Contains(content, `export ARO_HCP_E2E_SLOT_RESOURCE_TYPE='aro-hcp-dev-westus3-slot'`) {
		t.Fatalf("expected env file to contain ARO_HCP_E2E_SLOT_RESOURCE_TYPE export, got %q", content)
	}
	if !strings.Contains(content, `export CUSTOMER_SUBSCRIPTION='ARO HCP E2E Hosted Clusters (EA Subscription)'`) {
		t.Fatalf("expected env file to contain CUSTOMER_SUBSCRIPTION export, got %q", content)
	}
	if !strings.Contains(content, `export LEASED_MSI_CONTAINERS='aro-hcp-msi-container-dev-00-00 aro-hcp-msi-container-dev-00-01 aro-hcp-msi-container-dev-00-02'`) {
		t.Fatalf("expected env file to contain LEASED_MSI_CONTAINERS export, got %q", content)
	}
	if !strings.Contains(content, `export SELECTED_LOCATION='eastus2'`) {
		t.Fatalf("expected env file to contain SELECTED_LOCATION export, got %q", content)
	}
	if strings.Contains(content, "ARO_HCP_E2E_SLOT_REGION") {
		t.Fatalf("expected env file to omit ARO_HCP_E2E_SLOT_REGION export, got %q", content)
	}
	if strings.Contains(content, "ARO_HCP_E2E_SLOT_SUBSCRIPTION") {
		t.Fatalf("expected env file to omit ARO_HCP_E2E_SLOT_SUBSCRIPTION export, got %q", content)
	}
}

func TestLoadAcquiredSlotStateDefaultsRuntimeRegionToSlotRegion(t *testing.T) {
	t.Parallel()

	sharedDir := t.TempDir()
	state := `version: 1
deploy_environment: ci01
slot:
  environment: dev
  subscription_name: dev
  region: westus3
  resource_type: aro-hcp-dev-westus3-slot
  resource_name: aro-hcp-dev-westus3-slot-00
  slot_index: 0
  identity_container_prefix: aro-hcp-msi-container-dev-00
  identity_container_count: 1
leased_resource_name: aro-hcp-dev-westus3-slot-00
`

	stateFile, err := SlotStateFile(sharedDir)
	if err != nil {
		t.Fatalf("expected state file path to resolve: %v", err)
	}
	if err := os.WriteFile(stateFile, []byte(state), 0o644); err != nil {
		t.Fatalf("expected state file write to succeed: %v", err)
	}

	loadedState, err := LoadAcquiredSlotState(sharedDir)
	if err != nil {
		t.Fatalf("expected state load to succeed: %v", err)
	}
	if loadedState.RuntimeRegion != "westus3" {
		t.Fatalf("expected runtime region to default to slot region, got %q", loadedState.RuntimeRegion)
	}
}
