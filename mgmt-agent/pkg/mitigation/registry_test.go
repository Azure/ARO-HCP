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

package mitigation

import "testing"

type registryMitigator struct {
	name   string
	routes []string
}

func (m registryMitigator) Name() string               { return m.name }
func (m registryMitigator) DetectorNames() []string    { return m.routes }
func (registryMitigator) Plan(Input) (Decision, error) { return Decision{}, nil }

func TestRegistryValidation(t *testing.T) {
	valid := registryMitigator{name: "one", routes: []string{"fault"}}
	var typedNil *registryMitigator
	for _, test := range []struct {
		name       string
		mitigators []Mitigator
	}{
		{"nil", []Mitigator{nil}},
		{"typed nil", []Mitigator{typedNil}},
		{"empty name", []Mitigator{registryMitigator{routes: []string{"fault"}}}},
		{"no routes", []Mitigator{registryMitigator{name: "one"}}},
		{"empty route", []Mitigator{registryMitigator{name: "one", routes: []string{""}}}},
		{"duplicate name", []Mitigator{valid, valid}},
		{"duplicate route", []Mitigator{valid, registryMitigator{name: "two", routes: []string{"fault"}}}},
		{"repeated route", []Mitigator{registryMitigator{name: "one", routes: []string{"fault", "fault"}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewRegistry(test.mitigators...); err == nil {
				t.Fatal("invalid registry accepted")
			}
		})
	}
}

func TestRegistryOwnsRoutesAndNames(t *testing.T) {
	names := []string{"fault", "other"}
	list := []Mitigator{registryMitigator{name: "one", routes: names}}
	registry, err := NewRegistry(list...)
	if err != nil {
		t.Fatal(err)
	}
	list[0] = registryMitigator{name: "two", routes: []string{"new"}}
	names[0] = "new"
	for _, name := range []string{"fault", "other"} {
		if got := registry.ForDetector(name); got == nil || got.Name() != "one" {
			t.Fatalf("route %q changed: %v", name, got)
		}
	}
	if registry.ForDetector("new") != nil || registry.ByName("one") == nil || registry.ByName("two") != nil {
		t.Fatal("registry names or routes changed with the input slices")
	}
	empty, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if empty.ByName("one") != nil || empty.ForDetector("fault") != nil {
		t.Fatal("independent registry inherited registrations")
	}
}
