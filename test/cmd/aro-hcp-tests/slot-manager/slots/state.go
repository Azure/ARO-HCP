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
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	acquiredSlotStateVersionV1 = 1
	acquiredSlotStateVersionV2 = 2
)

type AcquiredSlotState struct {
	Leases             LeaseSet     `yaml:"leases,omitempty"`
	Version            int          `yaml:"version"`
	DeployEnvironment  string       `yaml:"deploy_environment"`
	RuntimeRegion      string       `yaml:"runtime_region"`
	Slot               ExpandedSlot `yaml:"slot"`
	LeasedResourceName string       `yaml:"leased_resource_name"`
}

type Lease struct {
	ResourceType string `yaml:"resource_type"`
	ResourceName string `yaml:"resource_name"`
	ReturnState  string `yaml:"return_state,omitempty"`
}

type LeaseSet struct {
	Primary Lease                 `yaml:"primary"`
	Assets  map[AssetKind][]Lease `yaml:"assets,omitempty"`
}

func EnsureStateDir(sharedDir string) (string, error) {
	stateDir, err := SharedStateDir(sharedDir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create state dir %q: %w", stateDir, err)
	}
	return stateDir, nil
}

// WriteAcquiredSlotState persists the release journal, including unresolved v2
// state. Runtime publication must validate the fully resolved state separately.
func WriteAcquiredSlotState(sharedDir string, state *AcquiredSlotState) error {
	if err := state.ValidateForRelease(); err != nil {
		return err
	}
	if _, err := EnsureStateDir(sharedDir); err != nil {
		return err
	}

	stateFile, err := SlotStateFile(sharedDir)
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal acquired slot state: %w", err)
	}
	if err := writeFileAtomically(stateFile, data, 0o644); err != nil {
		return fmt.Errorf("failed to write acquired slot state %q: %w", stateFile, err)
	}
	return nil
}

// LoadAcquiredSlotState loads the durable release journal. V2 state may be
// unresolved or already returning leases; call Validate before runtime use.
func LoadAcquiredSlotState(sharedDir string) (*AcquiredSlotState, error) {
	stateFile, err := SlotStateFile(sharedDir)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, err
	}

	state := &AcquiredSlotState{}
	if err := yaml.Unmarshal(data, state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal acquired slot state %q: %w", stateFile, err)
	}
	if err := state.ValidateForRelease(); err != nil {
		return nil, fmt.Errorf("invalid acquired slot state %q: %w", stateFile, err)
	}
	return state, nil
}

func RemoveStateFiles(sharedDir string) error {
	files := []func(string) (string, error){
		EnvFile,
		SlotStateFile,
	}

	errs := []error{}
	for _, filePathFunc := range files {
		path, err := filePathFunc(sharedDir)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("failed to remove %q: %w", path, err))
		}
	}

	return errors.Join(errs...)
}

// ValidateForRelease accepts partial v2 acquisition and return progress without
// depending on resolved runtime fields. V1 retains its legacy state validation.
func (s *AcquiredSlotState) ValidateForRelease() error {
	if s == nil {
		return errors.New("slot state is nil")
	}
	// V2 is a write-ahead lease journal. Resolution can be incomplete; release
	// must not depend on a catalog, credentials, handlers, or admission success.
	if s.Version == acquiredSlotStateVersionV2 {
		if strings.TrimSpace(s.Leases.Primary.ResourceName) == "" {
			return errors.New("slot state has empty primary lease")
		}
		seen := map[string]bool{}
		validate := func(lease Lease) error {
			if strings.TrimSpace(lease.ResourceName) == "" || strings.TrimSpace(lease.ResourceType) == "" {
				return errors.New("slot state contains incomplete lease")
			}
			if err := ValidateLeasedResourceName(lease.ResourceName); err != nil {
				return fmt.Errorf("slot state contains invalid lease: %w", err)
			}
			if seen[lease.ResourceName] {
				return fmt.Errorf("slot state contains duplicate lease %q", lease.ResourceName)
			}
			seen[lease.ResourceName] = true
			if lease.ReturnState != "" && lease.ReturnState != "returning" && lease.ReturnState != "returned" {
				return fmt.Errorf("lease %q has invalid return state %q", lease.ResourceName, lease.ReturnState)
			}
			return nil
		}
		if err := validate(s.Leases.Primary); err != nil {
			return err
		}
		for _, leases := range s.Leases.Assets {
			for _, lease := range leases {
				if err := validate(lease); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return s.Validate()
}

// Validate requires a fully resolved state suitable for runtime publication.
func (s *AcquiredSlotState) Validate() error {
	if s == nil {
		return errors.New("slot state is nil")
	}
	if s.Version != acquiredSlotStateVersionV1 && s.Version != acquiredSlotStateVersionV2 {
		return fmt.Errorf("unsupported slot state version %d", s.Version)
	}
	if s.Version == acquiredSlotStateVersionV2 {
		if err := s.ValidateForRelease(); err != nil {
			return err
		}
	}
	if strings.TrimSpace(s.RuntimeRegion) == "" {
		s.RuntimeRegion = strings.TrimSpace(s.Slot.Region)
	}

	switch {
	case strings.TrimSpace(s.DeployEnvironment) == "":
		return errors.New("slot state has empty deploy_environment")
	case strings.TrimSpace(s.RuntimeRegion) == "":
		return errors.New("slot state has empty runtime_region")
	case strings.TrimSpace(s.Slot.Environment) == "":
		return errors.New("slot state has empty slot environment")
	case strings.TrimSpace(s.Slot.ResourceType) == "":
		return errors.New("slot state has empty slot resource_type")
	case strings.TrimSpace(s.Slot.ResourceName) == "":
		return errors.New("slot state has empty slot resource_name")
	case strings.TrimSpace(s.LeasedResourceName) == "":
		return errors.New("slot state has empty leased_resource_name")
	}
	if s.Version == acquiredSlotStateVersionV2 {
		switch {
		case strings.TrimSpace(s.Slot.PoolName) == "":
			return errors.New("slot state has empty slot pool_name")
		case strings.TrimSpace(s.Slot.DeployEnvironment) == "":
			return errors.New("slot state has empty slot deploy_environment")
		case s.DeployEnvironment != s.Slot.DeployEnvironment:
			return errors.New("slot state deploy_environment does not match resolved slot")
		case s.Leases.Primary.ResourceName != s.Slot.ResourceName || s.LeasedResourceName != s.Slot.ResourceName:
			return errors.New("slot state primary lease name does not match resolved slot")
		case s.Leases.Primary.ResourceType != s.Slot.ResourceType:
			return errors.New("slot state primary lease type does not match resolved slot")
		case strings.TrimSpace(s.Slot.Subscriptions.E2E.Name) == "" || strings.TrimSpace(s.Slot.Subscriptions.E2E.ID) == "":
			return errors.New("slot state has unresolved E2E subscription")
		case s.Slot.RequiresInfrastructureSubscription() && (strings.TrimSpace(s.Slot.Subscriptions.Infrastructure.Name) == "" || strings.TrimSpace(s.Slot.Subscriptions.Infrastructure.ID) == ""):
			return errors.New("slot state has unresolved infrastructure subscription")
		case s.Leases.Primary.ReturnState != "":
			return errors.New("slot state primary lease is no longer held")
		}
		for _, leases := range s.Leases.Assets {
			for _, lease := range leases {
				if lease.ReturnState != "" {
					return fmt.Errorf("slot state asset lease %q is no longer held", lease.ResourceName)
				}
			}
		}
		return s.Slot.ValidateResolvedAssets()
	}
	return nil
}

func WriteEnvFile(sharedDir string, state *AcquiredSlotState, customerSubscription, selectedClusterProfileDir string) error {
	contract := NewRuntimeContractBuilder()
	if err := AddCoreRuntimeExports(contract, state, customerSubscription, selectedClusterProfileDir); err != nil {
		return err
	}
	if err := contract.Add("e2e-identities", "LEASED_MSI_CONTAINERS", strings.Join(state.Slot.IdentityContainerNames(), " ")); err != nil {
		return err
	}
	return WriteRuntimeContract(sharedDir, contract)
}

func AddCoreRuntimeExports(contract *RuntimeContractBuilder, state *AcquiredSlotState, customerSubscription, selectedClusterProfileDir string) error {
	if contract == nil {
		return errors.New("runtime contract builder is nil")
	}
	if err := state.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(customerSubscription) == "" {
		return errors.New("customer subscription is empty")
	}
	if strings.TrimSpace(selectedClusterProfileDir) == "" {
		return errors.New("selected cluster profile dir is empty")
	}
	coreExports := map[string]string{
		"ARO_HCP_E2E_SLOT_NAME":          state.Slot.ResourceName,
		"ARO_HCP_E2E_SLOT_RESOURCE_TYPE": state.Slot.ResourceType,
		"CUSTOMER_SUBSCRIPTION":          customerSubscription,
		"SELECTED_CLUSTER_PROFILE_DIR":   selectedClusterProfileDir,
		"SELECTED_LOCATION":              state.RuntimeRegion,
	}
	if state.Version == acquiredSlotStateVersionV2 {
		if customerSubscription != state.Slot.Subscriptions.E2E.Name {
			return errors.New("customer subscription does not match resolved slot")
		}
		coreExports["ARO_HCP_DEPLOY_ENV"] = state.Slot.DeployEnvironment
		if state.Slot.RequiresInfrastructureSubscription() {
			coreExports["INFRA_SUBSCRIPTION_ID"] = state.Slot.Subscriptions.Infrastructure.ID
		}
	}
	for key, value := range coreExports {
		if err := contract.Add("core", key, value); err != nil {
			return err
		}
	}
	return nil
}

func WriteRuntimeContract(sharedDir string, contract *RuntimeContractBuilder) error {
	if contract == nil {
		return errors.New("runtime contract builder is nil")
	}
	if _, err := EnsureStateDir(sharedDir); err != nil {
		return err
	}
	envFile, err := EnvFile(sharedDir)
	if err != nil {
		return err
	}
	data, err := contract.MarshalShell()
	if err != nil {
		return err
	}
	if err := writeFileAtomically(envFile, data, 0o644); err != nil {
		return fmt.Errorf("failed to write env file %q: %w", envFile, err)
	}
	return nil
}

type runtimeExport struct {
	owner string
	value string
}

type RuntimeContractBuilder struct {
	exports map[string]runtimeExport
}

func NewRuntimeContractBuilder() *RuntimeContractBuilder {
	return &RuntimeContractBuilder{exports: map[string]runtimeExport{}}
}

var shellIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (b *RuntimeContractBuilder) Add(owner, key, value string) error {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return errors.New("runtime export owner is empty")
	}
	if key == "" {
		return errors.New("runtime export key is empty")
	}
	if !shellIdentifier.MatchString(key) {
		return fmt.Errorf("runtime export key %q is invalid", key)
	}
	if b.exports == nil {
		b.exports = map[string]runtimeExport{}
	}
	if existing, found := b.exports[key]; found {
		return fmt.Errorf("runtime export %q is already owned by %q and cannot be published by %q", key, existing.owner, owner)
	}
	b.exports[key] = runtimeExport{owner: owner, value: value}
	return nil
}

func (b *RuntimeContractBuilder) MarshalShell() ([]byte, error) {
	keys := make([]string, 0, len(b.exports))
	for key := range b.exports {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var builder strings.Builder
	for _, key := range keys {
		value := strings.ReplaceAll(b.exports[key].value, "'", "'\"'\"'")
		if _, err := fmt.Fprintf(&builder, "export %s='%s'\n", key, value); err != nil {
			return nil, fmt.Errorf("failed building runtime contract: %w", err)
		}
	}
	return []byte(builder.String()), nil
}

func writeFileAtomically(path string, data []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()

	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
