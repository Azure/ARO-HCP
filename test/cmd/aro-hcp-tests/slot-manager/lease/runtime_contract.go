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

package lease

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
)

func AddCoreRuntimeExports(contract *RuntimeContractBuilder, state *AcquiredSlotState, customerSubscription, selectedClusterProfileDir string) error {
	if contract == nil {
		return errors.New("runtime contract builder is nil")
	}
	if err := state.ValidateForPublication(); err != nil {
		return err
	}
	if strings.TrimSpace(customerSubscription) == "" {
		return errors.New("customer subscription is empty")
	}
	if strings.TrimSpace(selectedClusterProfileDir) == "" {
		return errors.New("selected cluster profile dir is empty")
	}
	if err := state.Slot.VMFamilyPolicy.Validate(); err != nil {
		return fmt.Errorf("invalid slot vm_family_policy: %w", err)
	}
	policyJSON, err := json.Marshal(state.Slot.VMFamilyPolicy.Resolve(state.RuntimeRegion))
	if err != nil {
		return fmt.Errorf("failed to encode runtime VM family policy: %w", err)
	}
	coreExports := map[string]string{
		leaseenv.SlotName:                  state.Slot.ResourceName,
		leaseenv.SlotResourceType:          state.Slot.ResourceType,
		leaseenv.CustomerSubscription:      customerSubscription,
		leaseenv.SelectedClusterProfileDir: selectedClusterProfileDir,
		leaseenv.SelectedLocation:          state.RuntimeRegion,
		leaseenv.VMFamilyPolicy:            string(policyJSON),
	}
	if customerSubscription != state.Slot.Subscriptions.E2E.Name {
		return errors.New("customer subscription does not match resolved slot")
	}
	// Publication validation above already rejects an unresolved E2E
	// subscription, so consumers can address the subscription by ID instead of
	// re-reading a legacy subscription key from the cluster profile.
	coreExports[leaseenv.CustomerSubscriptionID] = state.Slot.Subscriptions.E2E.ID
	coreExports[leaseenv.RuntimeDeployEnvironment] = state.Slot.DeployEnvironment
	if state.Slot.RequiresInfrastructureSubscription() {
		coreExports[leaseenv.InfrastructureSubscriptionID] = state.Slot.Subscriptions.Infrastructure.ID
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
	if err := writeFileAtomically(envFile, contract.MarshalShell(), 0o644); err != nil {
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

// Add reserves an export and always replaces its value when the contract is sourced.
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

// MarshalShell emits safely quoted exports.
func (b *RuntimeContractBuilder) MarshalShell() []byte {
	keys := make([]string, 0, len(b.exports))
	for key := range b.exports {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var builder strings.Builder
	for _, key := range keys {
		export := b.exports[key]
		value := strings.ReplaceAll(export.value, "'", "'\"'\"'")
		fmt.Fprintf(&builder, "export %s='%s'\n", key, value)
	}
	return []byte(builder.String())
}
