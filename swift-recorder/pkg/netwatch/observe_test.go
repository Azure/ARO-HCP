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

import "testing"

func TestCompareLinkRename(t *testing.T) {
	a := LinkState{Ifindex: 5, Name: "eth0"}
	b := LinkState{Ifindex: 5, Name: "eth0-renamed"}
	obs := compareLinks(5, a, b)
	if len(obs) != 1 || obs[0].Kind != LinkRenamed {
		t.Fatalf("got %+v, want one LinkRenamed observation", obs)
	}
	if len(obs[0].Fields) != 1 || obs[0].Fields[0].From != "eth0" || obs[0].Fields[0].To != "eth0-renamed" {
		t.Fatalf("unexpected fields: %+v", obs[0].Fields)
	}
}

func TestCompareLinkPairingChange(t *testing.T) {
	a := LinkState{Ifindex: 7, MasterIndex: 0}
	b := LinkState{Ifindex: 7, MasterIndex: 3}
	obs := compareLinks(7, a, b)
	if len(obs) != 1 || obs[0].Kind != LinkPairingChanged {
		t.Fatalf("got %+v, want one LinkPairingChanged observation", obs)
	}
}

func TestCompareLinkStateAndConfigurationTogether(t *testing.T) {
	a := LinkState{Ifindex: 9, MTU: 1500, Carrier: true}
	b := LinkState{Ifindex: 9, MTU: 9000, Carrier: false}
	obs := compareLinks(9, a, b)
	kinds := map[ObservationKind]bool{}
	for _, o := range obs {
		kinds[o.Kind] = true
	}
	if len(obs) != 2 || !kinds[LinkStateChanged] || !kinds[LinkConfigurationChanged] {
		t.Fatalf("got %+v, want one LinkStateChanged and one LinkConfigurationChanged", obs)
	}
}
