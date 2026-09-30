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

//go:build linux

package netwatch

import "fmt"

// LinkState is the tracked, comparable configuration of one root-namespace
// link. Deliberately excluded: traffic counters (never projected here at
// all) and receipt/emission metadata (observed/logged separately as
// Observation evidence, not as compared state).
//
// Ifindex is the sole identity field: this link is tracked by root-namespace
// ifindex within one recorder session and boot, never by name (renames are a
// tracked change, not a new identity) and never by ParentDevice (a shared
// MANA PCI address does not identify one VF).
type LinkState struct {
	Ifindex int32  `json:"ifindex"`
	Name    string `json:"name"`
	MAC     string `json:"mac,omitempty"`
	// ParentBus/ParentDevice name the backing hardware: "vmbus" + a GUID for
	// an hv_netvsc synthetic, "pci" + a PCI address for a MANA/mlx5_core VF.
	// The PCI address is shared by every VF of the same physical function,
	// so it identifies the hardware family, never one VF.
	ParentBus    string `json:"parent_bus,omitempty"`
	ParentDevice string `json:"parent_device,omitempty"`
	// MasterIndex is this link's enslavement (IFLA_MASTER): nonzero while a
	// VF is paired to its synthetic. The kernel omits IFLA_MASTER entirely
	// when there is no master, and 0 is not a valid ifindex, so 0 here is
	// unambiguous - never "unknown", always "no current master".
	MasterIndex int32  `json:"master_index,omitempty"`
	ParentIndex int32  `json:"parent_index,omitempty"`
	MTU         uint32 `json:"mtu"`
	Up          bool   `json:"up"`
	Running     bool   `json:"running"`
	LowerUp     bool   `json:"lower_up"`
	Operstate   uint8  `json:"operstate"`
	Carrier     bool   `json:"carrier"`
}

// linkStateFromDecoded projects one rtnl.Decode-d RTM_GETLINK-schema entry
// into a LinkState. Returns an error if the mandatory ifindex is missing.
func linkStateFromDecoded(entry map[string]any) (LinkState, error) {
	ifindex, ok := entry["index"].(int32)
	if !ok {
		return LinkState{}, fmt.Errorf("decoded link missing index")
	}
	mtu, _ := entry["mtu"].(uint32)
	s := LinkState{
		Ifindex: ifindex,
		MTU:     mtu,
	}
	s.Name, _ = entry["name"].(string)
	s.MAC, _ = entry["mac"].(string)
	s.ParentBus, _ = entry["parentBus"].(string)
	s.ParentDevice, _ = entry["parentDevice"].(string)
	if v, ok := entry["masterIndex"].(uint32); ok {
		s.MasterIndex = int32(v)
	}
	if v, ok := entry["parentIndex"].(uint32); ok {
		s.ParentIndex = int32(v)
	}
	s.Up, _ = entry["up"].(bool)
	s.Running, _ = entry["running"].(bool)
	s.LowerUp, _ = entry["lowerUp"].(bool)
	s.Operstate, _ = entry["operstate"].(byte)
	s.Carrier, _ = entry["carrier"].(bool)
	return s, nil
}

// trackedLink is what the generation retains per currently-selected ifindex.
// Current is the compared state; LastKnownMasterIndex and
// LastKnownSyntheticGUID are retained pairing evidence that survives
// unpairing and synthetic departure so departure records can still
// correlate a VF to its former synthetic.
type trackedLink struct {
	Current                LinkState
	LastKnownMasterIndex   int32
	LastKnownSyntheticGUID string
}

// newTrackedLink creates a trackedLink from an initial state and records
// any pairing evidence it carries.
func newTrackedLink(state LinkState) *trackedLink {
	link := &trackedLink{}
	link.update(state)
	return link
}

// update replaces Current and maintains pairing evidence: a new positive
// MasterIndex is recorded, and a change in master clears the cached GUID
// so rememberPairings can re-resolve it.
func (link *trackedLink) update(state LinkState) {
	if state.MasterIndex > 0 {
		if state.MasterIndex != link.Current.MasterIndex {
			link.LastKnownSyntheticGUID = ""
		}
		link.LastKnownMasterIndex = state.MasterIndex
	}
	link.Current = state
}

// resolvePairings looks up the vmbus GUID for every VF that has a current
// master but no cached GUID yet. Call after any change to the link map or
// to a link's MasterIndex.
func resolvePairings(links map[int32]*trackedLink) {
	for _, link := range links {
		if link.Current.MasterIndex <= 0 || len(link.LastKnownSyntheticGUID) > 0 {
			continue
		}
		master, ok := links[link.Current.MasterIndex]
		if ok && master.Current.ParentBus == "vmbus" {
			link.LastKnownSyntheticGUID = master.Current.ParentDevice
		}
	}
}
