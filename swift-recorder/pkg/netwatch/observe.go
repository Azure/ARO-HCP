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

// ObservationKind names one category of tracked-state change.
type ObservationKind string

const (
	LinkAppeared             ObservationKind = "link_appeared"
	LinkDisappeared          ObservationKind = "link_disappeared"
	LinkRenamed              ObservationKind = "link_renamed"
	LinkPairingChanged       ObservationKind = "link_pairing_changed"
	LinkStateChanged         ObservationKind = "link_state_changed"
	LinkConfigurationChanged ObservationKind = "link_configuration_changed"
)

// FieldChange is one before/after field-level difference backing an
// Observation.
type FieldChange struct {
	Field string `json:"field"`
	From  any    `json:"from,omitempty"`
	To    any    `json:"to,omitempty"`
}

// Trigger carries evidence outside the tracked-state model itself.
type Trigger struct {
	Notification           string `json:"notification,omitempty"`
	NewNetnsID             *int32 `json:"new_netns_id,omitempty"`
	NewIfindex             *int32 `json:"new_ifindex,omitempty"`
	LastKnownMasterIndex   *int32 `json:"last_known_master_index,omitempty"`
	LastKnownSyntheticGUID string `json:"last_known_synthetic_guid,omitempty"`
}

// Observation is one named, structured change tied to a single interface.
type Observation struct {
	Kind    ObservationKind `json:"kind"`
	Ifindex int32           `json:"ifindex"`
	Fields  []FieldChange   `json:"fields,omitempty"`
	Trigger *Trigger        `json:"trigger,omitempty"`
	Before  *LinkState      `json:"before,omitempty"`
}

func compareLinks(ifindex int32, before, after LinkState) []Observation {
	var renamed, pairing, state, config []FieldChange

	if before.Name != after.Name {
		renamed = append(renamed, FieldChange{Field: "name", From: before.Name, To: after.Name})
	}
	if before.MasterIndex != after.MasterIndex {
		pairing = append(pairing, FieldChange{Field: "master_index", From: before.MasterIndex, To: after.MasterIndex})
	}
	if before.ParentIndex != after.ParentIndex {
		pairing = append(pairing, FieldChange{Field: "parent_index", From: before.ParentIndex, To: after.ParentIndex})
	}
	if before.Up != after.Up {
		state = append(state, FieldChange{Field: "up", From: before.Up, To: after.Up})
	}
	if before.Running != after.Running {
		state = append(state, FieldChange{Field: "running", From: before.Running, To: after.Running})
	}
	if before.LowerUp != after.LowerUp {
		state = append(state, FieldChange{Field: "lower_up", From: before.LowerUp, To: after.LowerUp})
	}
	if before.Operstate != after.Operstate {
		state = append(state, FieldChange{Field: "operstate", From: before.Operstate, To: after.Operstate})
	}
	if before.Carrier != after.Carrier {
		state = append(state, FieldChange{Field: "carrier", From: before.Carrier, To: after.Carrier})
	}
	if before.MTU != after.MTU {
		config = append(config, FieldChange{Field: "mtu", From: before.MTU, To: after.MTU})
	}
	if before.MAC != after.MAC {
		config = append(config, FieldChange{Field: "mac", From: before.MAC, To: after.MAC})
	}
	if before.ParentBus != after.ParentBus {
		config = append(config, FieldChange{Field: "parent_bus", From: before.ParentBus, To: after.ParentBus})
	}
	if before.ParentDevice != after.ParentDevice {
		config = append(config, FieldChange{Field: "parent_device", From: before.ParentDevice, To: after.ParentDevice})
	}

	var out []Observation
	if len(renamed) > 0 {
		out = append(out, Observation{Kind: LinkRenamed, Ifindex: ifindex, Fields: renamed})
	}
	if len(pairing) > 0 {
		out = append(out, Observation{Kind: LinkPairingChanged, Ifindex: ifindex, Fields: pairing})
	}
	if len(state) > 0 {
		out = append(out, Observation{Kind: LinkStateChanged, Ifindex: ifindex, Fields: state})
	}
	if len(config) > 0 {
		out = append(out, Observation{Kind: LinkConfigurationChanged, Ifindex: ifindex, Fields: config})
	}
	return out
}

func observeAppearance(link LinkState) Observation {
	return Observation{Kind: LinkAppeared, Ifindex: link.Ifindex}
}

// pairingEvidence copies link's retained last-known pairing evidence
// (master index and synthetic GUID) onto trigger, converting the
// nonzero-means-present LastKnownMasterIndex into Trigger's pointer field.
func pairingEvidence(trigger Trigger, link trackedLink) Trigger {
	if link.LastKnownMasterIndex != 0 {
		idx := link.LastKnownMasterIndex
		trigger.LastKnownMasterIndex = &idx
	}
	trigger.LastKnownSyntheticGUID = link.LastKnownSyntheticGUID
	return trigger
}

func observeDisappearance(link trackedLink, trigger Trigger) Observation {
	trigger = pairingEvidence(trigger, link)
	before := link.Current
	return Observation{Kind: LinkDisappeared, Ifindex: link.Current.Ifindex, Trigger: &trigger, Before: &before}
}

func enrichPairingObservations(obs []Observation, before trackedLink) {
	for i := range obs {
		if obs[i].Kind != LinkPairingChanged {
			continue
		}
		state := before.Current
		obs[i].Before = &state
		trigger := pairingEvidence(Trigger{Notification: "NEWLINK"}, before)
		obs[i].Trigger = &trigger
	}
}
