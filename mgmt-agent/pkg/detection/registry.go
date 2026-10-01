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

package detection

import (
	"fmt"
	"reflect"
	"slices"
)

// Registry evaluates detectors in registration order, preserving node-wide precedence.
type Registry struct {
	detectors []Detector
}

func NewRegistry(detectors ...Detector) (*Registry, error) {
	names := map[string]bool{}
	for _, detector := range detectors {
		if detector == nil || (reflect.ValueOf(detector).Kind() == reflect.Pointer && reflect.ValueOf(detector).IsNil()) {
			return nil, fmt.Errorf("nil detector")
		}
		if detector.Name() == "" || names[detector.Name()] {
			return nil, fmt.Errorf("invalid or duplicate detector")
		}
		names[detector.Name()] = true
		if detector.Reason() == "" || detector.Window() <= 0 {
			return nil, fmt.Errorf("detector %q requires a reason and positive window", detector.Name())
		}
		_, node := detector.(NodeDetector)
		_, pods := detector.(PodDetector)
		_, pod := detector.(PodScopedDetector)
		interfaces := 0
		for _, implemented := range []bool{node, pods, pod} {
			if implemented {
				interfaces++
			}
		}
		if interfaces != 1 || ((node || pods) && detector.Scope() != NodeScope) || (pod && detector.Scope() != PodScope) {
			return nil, fmt.Errorf("detector %q requires exactly one evaluation interface matching its scope", detector.Name())
		}
	}
	return &Registry{detectors: slices.Clone(detectors)}, nil
}
