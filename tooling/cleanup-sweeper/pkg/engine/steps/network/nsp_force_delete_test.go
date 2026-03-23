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

package network

import "testing"

func TestNSPForceDeleteStepConfig_StepOptions(t *testing.T) {
	t.Parallel()

	cfg := NSPForceDeleteStepConfig{
		Name:            "custom-name",
		Retries:         2,
		ContinueOnError: true,
	}

	opts := cfg.StepOptions()
	if opts.Name != cfg.Name {
		t.Fatalf("expected name %q, got %q", cfg.Name, opts.Name)
	}
	if opts.Retries != cfg.Retries {
		t.Fatalf("expected retries %d, got %d", cfg.Retries, opts.Retries)
	}
	if opts.ContinueOnError != cfg.ContinueOnError {
		t.Fatalf("expected continueOnError %t, got %t", cfg.ContinueOnError, opts.ContinueOnError)
	}
}

func TestNewNSPForceDeleteStep_DefaultName(t *testing.T) {
	t.Parallel()

	step := NewNSPForceDeleteStep(NSPForceDeleteStepConfig{})
	if got, want := step.Name(), "Delete network security perimeters"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
