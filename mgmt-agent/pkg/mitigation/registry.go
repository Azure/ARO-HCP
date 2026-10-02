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

import (
	"fmt"
	"reflect"
)

type Registry struct {
	routes map[string]Mitigator
	names  map[string]Mitigator
}

func (r *Registry) ForDetector(name string) Mitigator { return r.routes[name] }
func (r *Registry) ByName(name string) Mitigator      { return r.names[name] }

func NewRegistry(mitigators ...Mitigator) (*Registry, error) {
	routes := map[string]Mitigator{}
	names := map[string]Mitigator{}
	for _, mitigator := range mitigators {
		if mitigator == nil || (reflect.ValueOf(mitigator).Kind() == reflect.Pointer && reflect.ValueOf(mitigator).IsNil()) {
			return nil, fmt.Errorf("nil mitigator")
		}
		if mitigator.Name() == "" || names[mitigator.Name()] != nil {
			return nil, fmt.Errorf("invalid or duplicate mitigator")
		}
		names[mitigator.Name()] = mitigator
		if len(mitigator.DetectorNames()) == 0 {
			return nil, fmt.Errorf("mitigator %q requires at least one detector route", mitigator.Name())
		}
		for _, detector := range mitigator.DetectorNames() {
			if detector == "" || routes[detector] != nil {
				return nil, fmt.Errorf("invalid or conflicting detector route %q", detector)
			}
			routes[detector] = mitigator
		}
	}
	return &Registry{routes: routes, names: names}, nil
}
