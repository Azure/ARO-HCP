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

	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/detection"
)

var (
	swiftVFTeardown         = NewSwiftVFTeardown()
	cniPluginNotInitialized = NewCNIPluginNotInitialized()
	neverReady              = NewNeverReady()
)

func testDetectors() []detection.Detector {
	return []detection.Detector{
		NewSwiftVFTeardown(), NewCNIPluginNotInitialized(), NewNeverReady(), NewSwiftPodSandboxStalled(),
	}
}

func testRegistry(t testing.TB) *detection.Registry {
	t.Helper()
	registry, err := detection.NewRegistry(testDetectors()...)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func TestRegisteredDetectors(t *testing.T) {
	names := map[string]detection.Scope{}
	for _, d := range testDetectors() {
		if d == nil || d.Name() == "" || d.Reason() == "" || d.Window() <= 0 {
			t.Fatalf("invalid registered detector: %v", d)
		}
		if _, exists := names[d.Name()]; exists {
			t.Fatalf("duplicate detector %q", d.Name())
		}
		names[d.Name()] = d.Scope()
		kinds := 0
		if _, ok := d.(detection.NodeDetector); ok {
			kinds++
			if d.Scope() != detection.NodeScope {
				t.Fatalf("detection.NodeDetector %q must be node-scoped", d.Name())
			}
		}
		if _, ok := d.(detection.PodDetector); ok {
			kinds++
			if d.Scope() != detection.NodeScope {
				t.Fatalf("detection.PodDetector %q must be node-scoped", d.Name())
			}
		}
		if _, ok := d.(detection.PodScopedDetector); ok {
			kinds++
			if d.Scope() != detection.PodScope {
				t.Fatalf("detection.PodScopedDetector %q must be pod-scoped", d.Name())
			}
		}
		if kinds != 1 {
			t.Fatalf("detector %q implements %d evaluation interfaces, want exactly one", d.Name(), kinds)
		}
	}
	want := map[string]detection.Scope{
		"swift-vf-teardown":          detection.NodeScope,
		"cni-plugin-not-initialized": detection.NodeScope,
		"never-ready":                detection.NodeScope,
		SwiftPodSandboxStalled:       detection.PodScope,
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("registered detectors: got %v, want %v", names, want)
	}
}
