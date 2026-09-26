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

package detectors

import (
	"reflect"
	"testing"
)

func TestRegisteredDetectors(t *testing.T) {
	names := map[string]Scope{}
	for _, d := range registeredDetectors {
		if d == nil || d.Name() == "" || d.Reason() == "" || d.Window() <= 0 {
			t.Fatalf("invalid registered detector: %v", d)
		}
		if _, exists := names[d.Name()]; exists {
			t.Fatalf("duplicate detector %q", d.Name())
		}
		names[d.Name()] = d.Scope()
		kinds := 0
		if _, ok := d.(NodeDetector); ok {
			kinds++
			if d.Scope() != NodeScope {
				t.Fatalf("NodeDetector %q must be node-scoped", d.Name())
			}
		}
		if _, ok := d.(PodDetector); ok {
			kinds++
			if d.Scope() != NodeScope {
				t.Fatalf("PodDetector %q must be node-scoped", d.Name())
			}
		}
		if _, ok := d.(PodScopedDetector); ok {
			kinds++
			if d.Scope() != PodScope {
				t.Fatalf("PodScopedDetector %q must be pod-scoped", d.Name())
			}
		}
		if kinds != 1 {
			t.Fatalf("detector %q implements %d evaluation interfaces, want exactly one", d.Name(), kinds)
		}
	}
	want := map[string]Scope{
		"swift-vf-teardown":          NodeScope,
		"cni-plugin-not-initialized": NodeScope,
		"never-ready":                NodeScope,
		SwiftPodSandboxStalled:       PodScope,
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("registered detectors: got %v, want %v", names, want)
	}
}
