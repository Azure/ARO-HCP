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

package cijoboutcomes

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const slotStateArtifactPath = "aro-hcp-lease-acquire/artifacts/aro-hcp-slot-state.yaml"

const (
	slotStateAvailable   = "available"
	slotStateAbsent      = "absent"
	slotStateMalformed   = "malformed"
	slotStateUnsupported = "unsupported-version"
)

var errSlotStateVersion = errors.New("unsupported slot state version")

type leasedAsset struct {
	Kind         string `json:"kind"`
	ResourceName string `json:"resourceName"`
}

type slotStateMetadata struct {
	RuntimeRegion string
	LeasedSlot    string
	LeasedAssets  []leasedAsset
	Version       *int32
	Status        string
}

// This is a projection of the acquisition artifact, not the slot manager's
// release journal API. Reading historical facts must not require today's catalog.
type slotStateLease struct {
	ResourceType string `yaml:"resource_type"`
	ResourceName string `yaml:"resource_name"`
	ReturnState  string `yaml:"return_state"`
}

func parseSlotState(data []byte) (slotStateMetadata, error) {
	var metadata slotStateMetadata
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&document); err != nil {
		return metadata, fmt.Errorf("decode slot state: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return metadata, errors.New("slot state must contain exactly one YAML document")
	}
	var header struct {
		Version *int32 `yaml:"version"`
	}
	if err := document.Decode(&header); err != nil {
		return metadata, fmt.Errorf("decode slot state version: %w", err)
	}
	if header.Version == nil || *header.Version <= 0 {
		return metadata, errors.New("slot state has no positive version")
	}
	metadata.Version = header.Version
	if *header.Version != 2 {
		return metadata, fmt.Errorf("%w: %d", errSlotStateVersion, *header.Version)
	}
	var state struct {
		RuntimeRegion      string `yaml:"runtime_region"`
		LeasedResourceName string `yaml:"leased_resource_name"`
		Leases             struct {
			Primary slotStateLease               `yaml:"primary"`
			Assets  map[string][]*slotStateLease `yaml:"assets"`
		} `yaml:"leases"`
		Slot struct {
			ResourceType string `yaml:"resource_type"`
			ResourceName string `yaml:"resource_name"`
		} `yaml:"slot"`
	}
	if err := document.Decode(&state); err != nil {
		return metadata, fmt.Errorf("decode slot state leases: %w", err)
	}
	if strings.TrimSpace(state.RuntimeRegion) == "" {
		return metadata, errors.New("slot state has no runtime region")
	}
	seen := map[string]bool{}
	validateLease := func(lease slotStateLease) error {
		if strings.TrimSpace(lease.ResourceType) == "" || strings.TrimSpace(lease.ResourceName) == "" {
			return errors.New("slot state contains an incomplete lease")
		}
		if lease.ReturnState != "" {
			return errors.New("slot state contains a lease that is no longer held")
		}
		if seen[lease.ResourceName] {
			return errors.New("slot state contains a duplicate lease")
		}
		seen[lease.ResourceName] = true
		return nil
	}
	primary := state.Leases.Primary
	if err := validateLease(primary); err != nil {
		return metadata, err
	}
	if primary.ResourceName != state.LeasedResourceName ||
		primary.ResourceName != state.Slot.ResourceName ||
		primary.ResourceType != state.Slot.ResourceType {
		return metadata, errors.New("slot state primary lease does not match the resolved slot")
	}
	assets := make([]leasedAsset, 0)
	for kind, leases := range state.Leases.Assets {
		if strings.TrimSpace(kind) == "" {
			return metadata, errors.New("slot state contains an empty asset kind")
		}
		for _, lease := range leases {
			if lease == nil {
				return metadata, errors.New("slot state contains a null lease")
			}
			if err := validateLease(*lease); err != nil {
				return metadata, err
			}
			assets = append(assets, leasedAsset{Kind: kind, ResourceName: lease.ResourceName})
		}
	}
	slices.SortFunc(assets, func(a, b leasedAsset) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.ResourceName, b.ResourceName))
	})
	metadata.RuntimeRegion = state.RuntimeRegion
	metadata.LeasedSlot = primary.ResourceName
	metadata.LeasedAssets = assets
	metadata.Status = slotStateAvailable
	return metadata, nil
}
