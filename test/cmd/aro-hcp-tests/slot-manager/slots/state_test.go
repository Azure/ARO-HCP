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
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestWriteAndLoadAcquiredSlotStateAndEnvFile(t *testing.T) {
	t.Parallel()

	sharedDir := t.TempDir()
	state := &AcquiredSlotState{
		Version:           acquiredSlotStateVersionV1,
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
	if err := WriteEnvFile(sharedDir, state, "ARO HCP E2E Hosted Clusters (EA Subscription)", "/var/run/aro-hcp-dev"); err != nil {
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
	if !strings.Contains(content, `export SELECTED_CLUSTER_PROFILE_DIR='/var/run/aro-hcp-dev'`) {
		t.Fatalf("expected env file to contain SELECTED_CLUSTER_PROFILE_DIR export, got %q", content)
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

func TestWriteV2AcquiredSlotStateAndRuntimeContract(t *testing.T) {
	t.Parallel()

	sharedDir := t.TempDir()
	state := resolvedV2TestState()
	state.Slot.Requirements = []AssetRequirement{
		{Kind: KindInfrastructureIdentities, Allocation: AllocationLeased, UnitsPerSlot: 1},
	}
	state.Slot.Assets = ResolvedAssets{
		InfrastructureIdentities: &ResolvedInfrastructureIdentities{
			Allocation: AllocationLeased, ResourceGroups: []string{"bundle-00"}, Identities: []string{"service"},
		},
		E2EIdentities: &ResolvedE2EIdentitiesAsset{
			ProvisioningRegion: "westus3",
			ResourceGroups:     []string{"identity-rg-00", "identity-rg-01"},
		},
	}

	if err := WriteAcquiredSlotState(sharedDir, state); err != nil {
		t.Fatalf("expected v2 state write to succeed: %v", err)
	}
	if err := WriteEnvFile(sharedDir, state, "dev-e2e", "/var/run/aro-hcp-dev"); err != nil {
		t.Fatalf("expected v2 runtime contract write to succeed: %v", err)
	}
	envPath, err := EnvFile(sharedDir)
	if err != nil {
		t.Fatalf("expected runtime contract path resolution to succeed: %v", err)
	}
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("expected runtime contract read to succeed: %v", err)
	}
	content := string(data)
	for _, expected := range []string{
		"export ARO_HCP_DEPLOY_ENV='ci01'",
		"export INFRA_SUBSCRIPTION_ID='infra-id'",
		"export LEASED_MSI_CONTAINERS='identity-rg-00 identity-rg-01'",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("expected runtime contract to contain %q, got %q", expected, content)
		}
	}
}

func TestRuntimeContractRejectsExportCollisions(t *testing.T) {
	t.Parallel()

	contract := NewRuntimeContractBuilder()
	if err := contract.Add("core", "SHARED_VALUE", "one"); err != nil {
		t.Fatalf("expected first export to succeed: %v", err)
	}

	err := contract.Add("asset", "SHARED_VALUE", "two")
	if err == nil || !strings.Contains(err.Error(), "already owned") {
		t.Fatalf("expected export collision error, got %v", err)
	}
}

func TestE2EOnlyStateAndRuntimeOmitInfrastructure(t *testing.T) {
	t.Parallel()
	for _, infrastructure := range []ResolvedSubscription{{}, {Name: "unused", ID: "unused-id"}} {
		state := resolvedV2TestState()
		state.Slot.Subscriptions.Infrastructure = infrastructure
		state.Slot.Requirements = []AssetRequirement{{Kind: KindE2EIdentities, Allocation: AllocationDedicated}}
		state.Slot.IdentityContainerPrefix = "identities-00"
		state.Slot.IdentityContainerCount = 1
		state.Slot.Assets.E2EIdentities = &ResolvedE2EIdentitiesAsset{
			Allocation: AllocationDedicated, ResourceGroups: []string{"identities-00-00"},
		}
		if err := state.Validate(); err != nil {
			t.Fatalf("E2E-only state required infrastructure: %v", err)
		}
		contract := NewRuntimeContractBuilder()
		if err := AddCoreRuntimeExports(contract, state, "dev-e2e", "profile"); err != nil {
			t.Fatal(err)
		}
		if data := contract.MarshalShell(); strings.Contains(string(data), "INFRA_SUBSCRIPTION_ID") {
			t.Fatalf("E2E-only runtime contract exported infrastructure: %s", data)
		}
	}
}

func TestRuntimeContractShellIdentifiers(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"NAME", "_name", "Name_01"} {
		if err := NewRuntimeContractBuilder().Add("asset", key, "value"); err != nil {
			t.Errorf("valid identifier %q rejected: %v", key, err)
		}
	}
	// Test the validator directly; never execute the rejected shell syntax.
	for _, key := range []string{"", "0NAME", " NAME", "NAME ", "A-B", "A.B", "A;B", "A$(id)", "A`id`", "A/B", "A\\B", "A(B)", "A[0]", "A|B", "A&B", "A\nB", "é", "NAME=value"} {
		if err := NewRuntimeContractBuilder().Add("asset", key, "value"); err == nil {
			t.Errorf("unsafe identifier %q accepted", key)
		}
	}
	contract := NewRuntimeContractBuilder()
	if err := contract.Add("asset", "VALUE", "one'$(not-executed)"); err != nil {
		t.Fatal(err)
	}
	if data := contract.MarshalShell(); string(data) != "export VALUE='one'\"'\"'$(not-executed)'\n" {
		t.Fatalf("value was not shell escaped: %q", data)
	}
}

func TestAcquiredSlotStateRejectsInvalidJournals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*AcquiredSlotState)
		want   string
	}{
		{"unsupported version", func(s *AcquiredSlotState) { s.Version = 3 }, "unsupported slot state version"},
		{"missing primary", func(s *AcquiredSlotState) { s.Leases.Primary = Lease{} }, "empty primary lease"},
		{"blank primary", func(s *AcquiredSlotState) { s.Leases.Primary.ResourceName = " " }, "empty primary lease"},
		{"padded primary", func(s *AcquiredSlotState) { s.Leases.Primary.ResourceName = " slot-00 " }, "leading or trailing whitespace"},
		{"missing type", func(s *AcquiredSlotState) { s.Leases.Primary.ResourceType = "" }, "incomplete lease"},
		{"blank type", func(s *AcquiredSlotState) { s.Leases.Primary.ResourceType = " " }, "incomplete lease"},
		{"invalid return state", func(s *AcquiredSlotState) { s.Leases.Primary.ReturnState = "invalid" }, "invalid return state"},
		{"incomplete asset", func(s *AcquiredSlotState) {
			s.Leases.Assets = map[AssetKind][]Lease{"unknown-kind": {{ResourceType: "bundle", ResourceName: " "}}}
		}, "incomplete lease"},
		{"padded asset", func(s *AcquiredSlotState) {
			s.Leases.Assets = map[AssetKind][]Lease{"unknown-kind": {{ResourceType: "bundle", ResourceName: " bundle-00 "}}}
		}, "leading or trailing whitespace"},
		{"duplicate lease", func(s *AcquiredSlotState) {
			s.Leases.Assets = map[AssetKind][]Lease{"unknown-kind": {s.Leases.Primary}}
		}, "duplicate lease"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := resolvedV2TestState()
			tc.mutate(state)
			for _, validate := range []func() error{state.Validate, state.ValidateForRelease} {
				if err := validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("expected error %q, got %v", tc.want, err)
				}
			}
			if err := WriteAcquiredSlotState(t.TempDir(), state); err == nil {
				t.Fatal("invalid journal must not be persisted")
			}
		})
	}
	dir := t.TempDir()
	stateFile, err := SlotStateFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile, []byte("version: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAcquiredSlotState(dir); err == nil || !strings.Contains(err.Error(), "unsupported slot state version") {
		t.Fatalf("load must reject unsupported journal versions: %v", err)
	}
}

func resolvedV2TestState() *AcquiredSlotState {
	return &AcquiredSlotState{
		Version:            acquiredSlotStateVersionV2,
		DeployEnvironment:  "ci01",
		RuntimeRegion:      "centralus",
		LeasedResourceName: "slot-00",
		Leases:             LeaseSet{Primary: Lease{ResourceType: "slot", ResourceName: "slot-00"}},
		Slot: ExpandedSlot{
			Environment:       "dev",
			PoolName:          "shard0",
			DeployEnvironment: "ci01",
			ResourceType:      "slot",
			ResourceName:      "slot-00",
			Subscriptions: ResolvedSubscriptions{
				E2E:            ResolvedSubscription{Name: "dev-e2e", ID: "e2e-id"},
				Infrastructure: ResolvedSubscription{Name: "dev-infra", ID: "infra-id"},
			},
		},
	}
}

func TestAcquiredSlotStateSeparatesJournalAndRuntimeValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*AcquiredSlotState)
		want   string
	}{
		{"partial journal", func(s *AcquiredSlotState) { s.Slot = ExpandedSlot{} }, "empty slot environment"},
		{"deploy environment", func(s *AcquiredSlotState) { s.DeployEnvironment = " " }, "empty deploy_environment"},
		{"runtime region", func(s *AcquiredSlotState) { s.RuntimeRegion = " " }, "empty runtime_region"},
		{"slot environment", func(s *AcquiredSlotState) { s.Slot.Environment = " " }, "empty slot environment"},
		{"slot type", func(s *AcquiredSlotState) { s.Slot.ResourceType = " " }, "empty slot resource_type"},
		{"slot name", func(s *AcquiredSlotState) { s.Slot.ResourceName = " " }, "empty slot resource_name"},
		{"leased name", func(s *AcquiredSlotState) { s.LeasedResourceName = " " }, "empty leased_resource_name"},
		{"pool name", func(s *AcquiredSlotState) { s.Slot.PoolName = " " }, "empty slot pool_name"},
		{"slot deploy environment", func(s *AcquiredSlotState) { s.Slot.DeployEnvironment = " " }, "empty slot deploy_environment"},
		{"deploy mismatch", func(s *AcquiredSlotState) { s.Slot.DeployEnvironment = "ci02" }, "deploy_environment does not match"},
		{"journal name mismatch", func(s *AcquiredSlotState) { s.Leases.Primary.ResourceName = "slot-01" }, "primary lease name does not match"},
		{"legacy name mismatch", func(s *AcquiredSlotState) { s.LeasedResourceName = "slot-01" }, "primary lease name does not match"},
		{"journal type mismatch", func(s *AcquiredSlotState) { s.Leases.Primary.ResourceType = "other" }, "primary lease type does not match"},
		{"e2e name", func(s *AcquiredSlotState) { s.Slot.Subscriptions.E2E.Name = " " }, "unresolved E2E subscription"},
		{"e2e ID", func(s *AcquiredSlotState) { s.Slot.Subscriptions.E2E.ID = " " }, "unresolved E2E subscription"},
		{"infra name", func(s *AcquiredSlotState) {
			s.Slot.Requirements = []AssetRequirement{{Kind: KindInfrastructureIdentities}}
			s.Slot.Subscriptions.Infrastructure.Name = " "
		}, "unresolved infrastructure subscription"},
		{"infra ID", func(s *AcquiredSlotState) {
			s.Slot.Requirements = []AssetRequirement{{Kind: KindInfrastructureIdentities}}
			s.Slot.Subscriptions.Infrastructure.ID = " "
		}, "unresolved infrastructure subscription"},
		{"primary returning", func(s *AcquiredSlotState) { s.Leases.Primary.ReturnState = "returning" }, "primary lease is no longer held"},
		{"primary returned", func(s *AcquiredSlotState) { s.Leases.Primary.ReturnState = "returned" }, "primary lease is no longer held"},
		{"asset returning", func(s *AcquiredSlotState) {
			s.Leases.Assets = map[AssetKind][]Lease{"unknown-kind": {{ResourceType: "bundle", ResourceName: "bundle-00", ReturnState: "returning"}}}
		}, "asset lease \"bundle-00\" is no longer held"},
		{"asset returned", func(s *AcquiredSlotState) {
			s.Leases.Assets = map[AssetKind][]Lease{"unknown-kind": {{ResourceType: "bundle", ResourceName: "bundle-00", ReturnState: "returned"}}}
		}, "asset lease \"bundle-00\" is no longer held"},
		{"unresolved assets", func(s *AcquiredSlotState) {
			s.Slot.Requirements = []AssetRequirement{{Kind: KindE2EIdentities}}
		}, "unresolved demanded asset"},
		{"truncated dedicated identities", func(s *AcquiredSlotState) {
			s.Slot.Requirements = []AssetRequirement{{Kind: KindE2EIdentities, Allocation: AllocationDedicated}}
			s.Slot.IdentityContainerPrefix, s.Slot.IdentityContainerCount = "identities-00", 2
			s.Slot.Assets.E2EIdentities = &ResolvedE2EIdentitiesAsset{
				Allocation: AllocationDedicated, ResourceGroups: []string{"identities-00-00"},
			}
		}, "does not match dedicated identity containers"},
		{"foreign dedicated identity", func(s *AcquiredSlotState) {
			s.Slot.Requirements = []AssetRequirement{{Kind: KindE2EIdentities, Allocation: AllocationDedicated}}
			s.Slot.IdentityContainerPrefix, s.Slot.IdentityContainerCount = "identities-00", 1
			s.Slot.Assets.E2EIdentities = &ResolvedE2EIdentitiesAsset{
				Allocation: AllocationDedicated, ResourceGroups: []string{"foreign-00-00"},
			}
		}, "does not match dedicated identity containers"},
		{"duplicate dedicated identities", func(s *AcquiredSlotState) {
			s.Slot.Requirements = []AssetRequirement{{Kind: KindE2EIdentities, Allocation: AllocationDedicated}}
			s.Slot.IdentityContainerPrefix, s.Slot.IdentityContainerCount = "identities-00", 2
			s.Slot.Assets.E2EIdentities = &ResolvedE2EIdentitiesAsset{
				Allocation: AllocationDedicated, ResourceGroups: []string{"identities-00-00", "identities-00-00"},
			}
		}, "does not match dedicated identity containers"},
		{"wrong resolved identity allocation", func(s *AcquiredSlotState) {
			s.Slot.Requirements = []AssetRequirement{{Kind: KindE2EIdentities, Allocation: AllocationDedicated}}
			s.Slot.IdentityContainerPrefix, s.Slot.IdentityContainerCount = "identities-00", 1
			s.Slot.Assets.E2EIdentities = &ResolvedE2EIdentitiesAsset{
				Allocation: AllocationLeased, ResourceGroups: []string{"identities-00-00"},
			}
		}, "requires dedicated allocation"},
		{"wrong demanded identity allocation", func(s *AcquiredSlotState) {
			s.Slot.Requirements = []AssetRequirement{{Kind: KindE2EIdentities, Allocation: AllocationLeased}}
			s.Slot.IdentityContainerPrefix, s.Slot.IdentityContainerCount = "identities-00", 1
			s.Slot.Assets.E2EIdentities = &ResolvedE2EIdentitiesAsset{
				Allocation: AllocationDedicated, ResourceGroups: []string{"identities-00-00"},
			}
		}, "requires dedicated allocation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := resolvedV2TestState()
			tc.mutate(state)
			if err := state.ValidateForRelease(); err != nil {
				t.Fatalf("runtime failure must not prevent release: %v", err)
			}
			if err := state.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected runtime validation error %q, got %v", tc.want, err)
			}
			dir := t.TempDir()
			if err := WriteAcquiredSlotState(dir, state); err != nil {
				t.Fatalf("runtime failure must not prevent journal persistence: %v", err)
			}
			loaded, err := LoadAcquiredSlotState(dir)
			if err != nil || !reflect.DeepEqual(loaded.Leases, state.Leases) {
				t.Fatalf("failed to reload release journal: %+v, %v", loaded, err)
			}
			contract := NewRuntimeContractBuilder()
			if err := AddCoreRuntimeExports(contract, state, "dev-e2e", "profile"); err == nil {
				t.Fatal("invalid runtime state published core exports")
			}
			if data := contract.MarshalShell(); len(data) != 0 {
				t.Fatalf("failed validation partially published core exports: %q", data)
			}
			if err := WriteEnvFile(dir, state, "dev-e2e", "profile"); err == nil {
				t.Fatal("invalid runtime state published an env file")
			}
			envFile, err := EnvFile(dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(envFile); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid runtime state created an env file: %v", err)
			}
		})
	}
}

func TestAcquiredSlotStateValidationVersionsAndRegionFallback(t *testing.T) {
	t.Parallel()
	for _, version := range []int{acquiredSlotStateVersionV1, acquiredSlotStateVersionV2} {
		for _, region := range []string{"", " \t", "eastus2"} {
			state := resolvedV2TestState()
			state.Version = version
			state.RuntimeRegion = region
			state.Slot.Region = "westus3"
			if version == acquiredSlotStateVersionV1 {
				state.Leases = LeaseSet{}
				state.Slot.PoolName = ""
				state.Slot.DeployEnvironment = ""
				state.Slot.Subscriptions = ResolvedSubscriptions{}
			}
			if err := state.Validate(); err != nil {
				t.Fatalf("v%d valid state rejected: %v", version, err)
			}
			want := region
			if strings.TrimSpace(want) == "" {
				want = state.Slot.Region
			}
			if state.RuntimeRegion != want {
				t.Fatalf("v%d runtime region: got %q, want %q", version, state.RuntimeRegion, want)
			}
		}
	}
	for _, version := range []int{-1, 0, 3} {
		state := resolvedV2TestState()
		state.Version = version
		for _, validate := range []func() error{state.Validate, state.ValidateForRelease} {
			if err := validate(); err == nil || !strings.Contains(err.Error(), "unsupported slot state version") {
				t.Fatalf("version %d was not rejected: %v", version, err)
			}
		}
	}
	var state *AcquiredSlotState
	if state.Validate() == nil || state.ValidateForRelease() == nil {
		t.Fatal("nil state must fail both validators")
	}
}

func TestRuntimePublicationValidatesInputs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		customer string
		profile  string
		want     string
	}{
		{" ", "profile", "customer subscription is empty"},
		{"dev-e2e", " ", "selected cluster profile dir is empty"},
		{"other-e2e", "profile", "customer subscription does not match"},
	} {
		state := resolvedV2TestState()
		err := AddCoreRuntimeExports(NewRuntimeContractBuilder(), state, tc.customer, tc.profile)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("expected publication error %q, got %v", tc.want, err)
		}
	}
	if err := AddCoreRuntimeExports(NewRuntimeContractBuilder(), nil, "dev-e2e", "profile"); err == nil {
		t.Fatal("nil state must fail publication without panicking")
	}
}
