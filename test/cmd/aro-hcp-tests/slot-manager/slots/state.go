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
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const acquiredSlotStateVersion = 1

type AcquiredSlotState struct {
	Version            int          `yaml:"version"`
	DeployEnvironment  string       `yaml:"deploy_environment"`
	RuntimeRegion      string       `yaml:"runtime_region"`
	Slot               ExpandedSlot `yaml:"slot"`
	LeasedResourceName string       `yaml:"leased_resource_name"`
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

func WriteAcquiredSlotState(sharedDir string, state *AcquiredSlotState) error {
	if err := state.Validate(); err != nil {
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
	if err := os.WriteFile(stateFile, data, 0o644); err != nil {
		return fmt.Errorf("failed to write acquired slot state %q: %w", stateFile, err)
	}
	return nil
}

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
	if err := state.Validate(); err != nil {
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

func (s *AcquiredSlotState) Validate() error {
	if s == nil {
		return errors.New("slot state is nil")
	}

	if strings.TrimSpace(s.RuntimeRegion) == "" {
		s.RuntimeRegion = strings.TrimSpace(s.Slot.Region)
	}

	switch {
	case s.Version != acquiredSlotStateVersion:
		return fmt.Errorf("unsupported slot state version %d", s.Version)
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
	return nil
}

func WriteEnvFile(sharedDir string, state *AcquiredSlotState, customerSubscription string) error {
	if err := state.Validate(); err != nil {
		return err
	}
	if customerSubscription == "" {
		return errors.New("customer subscription is empty")
	}
	if _, err := EnsureStateDir(sharedDir); err != nil {
		return err
	}

	envFile, err := EnvFile(sharedDir)
	if err != nil {
		return err
	}

	exports := map[string]string{
		"ARO_HCP_E2E_SLOT_NAME":          state.Slot.ResourceName,
		"ARO_HCP_E2E_SLOT_RESOURCE_TYPE": state.Slot.ResourceType,
		"CUSTOMER_SUBSCRIPTION":          customerSubscription,
		"LEASED_MSI_CONTAINERS":          strings.Join(state.Slot.IdentityContainerNames(), " "),
		"SELECTED_LOCATION":              state.RuntimeRegion,
	}

	var builder strings.Builder
	for _, key := range sortedKeys(exports) {
		_, _ = fmt.Fprintf(&builder, "export %s='%s'\n", key, exports[key])
	}

	if err := os.WriteFile(envFile, []byte(builder.String()), 0o644); err != nil {
		return fmt.Errorf("failed to write env file %q: %w", envFile, err)
	}
	return nil
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
