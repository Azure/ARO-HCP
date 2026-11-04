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

package main

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	"github.com/openshift-eng/openshift-tests-extension/pkg/util/sets"

	"github.com/Azure/ARO-HCP/test/util/mipool"
)

type miPoolTestSpec struct {
	name    string
	label   string
	result  et.Result
	envSeen map[string]string
}

func TestWireMIPoolHandsContainersToChildAndReleases(t *testing.T) {
	var mu sync.Mutex
	cleaned := []string{}

	tests := []*miPoolTestSpec{
		{name: "needs-two", label: "MIContainers:2", result: et.ResultPassed},
		{name: "needs-one-fails", label: "MIContainers:1", result: et.ResultFailed},
		{name: "needs-none", label: "MIContainers:0", result: et.ResultPassed},
	}

	specs := et.ExtensionTestSpecs{}
	for _, tt := range tests {
		spec := &et.ExtensionTestSpec{Name: tt.name, Labels: sets.New(tt.label)}
		// The result deliberately omits Name, as the runner is not guaranteed to set it.
		spec.RunParallel = func(context.Context) *et.ExtensionTestResult {
			mu.Lock()
			defer mu.Unlock()
			tt.envSeen = map[string]string{}
			for k, v := range spec.Env {
				tt.envSeen[k] = v
			}
			return &et.ExtensionTestResult{Result: tt.result}
		}
		specs = append(specs, spec)
	}

	pool, err := mipool.New("rg-a rg-b rg-c")
	if err != nil {
		t.Fatalf("mipool.New: %v", err)
	}
	wireMIPool(specs, pool, func(rg string) error {
		mu.Lock()
		defer mu.Unlock()
		cleaned = append(cleaned, rg)
		return nil
	})

	// Concurrency 1 keeps the container assignment deterministic.
	if _, err := specs.Run(context.Background(), et.NullResultWriter{}, 1); err == nil {
		t.Fatalf("expected the failing spec to surface as a run error")
	}

	two := strings.Fields(tests[0].envSeen[mipool.AssignedMIContainersEnvvar])
	if len(two) != 2 {
		t.Errorf("needs-two: child saw %q, want 2 containers", tests[0].envSeen[mipool.AssignedMIContainersEnvvar])
	}
	one := strings.Fields(tests[1].envSeen[mipool.AssignedMIContainersEnvvar])
	if len(one) != 1 {
		t.Errorf("needs-one-fails: child saw %q, want 1 container", tests[1].envSeen[mipool.AssignedMIContainersEnvvar])
	}
	if _, ok := tests[2].envSeen[mipool.AssignedMIContainersEnvvar]; ok {
		t.Errorf("needs-none: child must not receive %s", mipool.AssignedMIContainersEnvvar)
	}

	if pool.Free() != pool.Size() {
		t.Errorf("pool has %d free of %d after run; containers were not released", pool.Free(), pool.Size())
	}
	if len(pool.Quarantined()) != 0 {
		t.Errorf("unexpected quarantined containers: %v", pool.Quarantined())
	}
	// Only the failed test is cleaned up by the parent.
	if !slices.Equal(cleaned, one) {
		t.Errorf("parent cleaned %v, want only the failed test's containers %v", cleaned, one)
	}
}

func TestWireMIPoolSingleSpecRunsInProcessWithContainers(t *testing.T) {
	t.Setenv(mipool.AssignedMIContainersEnvvar, "")
	var seen string
	spec := &et.ExtensionTestSpec{Name: "only-one", Labels: sets.New("MIContainers:1")}
	spec.Run = func(context.Context) *et.ExtensionTestResult {
		seen = os.Getenv(mipool.AssignedMIContainersEnvvar)
		return &et.ExtensionTestResult{Result: et.ResultPassed}
	}
	specs := et.ExtensionTestSpecs{spec}

	pool, err := mipool.New("rg-a rg-b")
	if err != nil {
		t.Fatalf("mipool.New: %v", err)
	}
	wireMIPool(specs, pool, func(string) error { return nil })

	if _, err := specs.Run(context.Background(), et.NullResultWriter{}, 1); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.Fields(seen); len(got) != 1 {
		t.Errorf("single spec saw %q, want 1 container", seen)
	}
	if pool.Free() != pool.Size() {
		t.Errorf("pool has %d free of %d after run; containers were not released", pool.Free(), pool.Size())
	}
}
