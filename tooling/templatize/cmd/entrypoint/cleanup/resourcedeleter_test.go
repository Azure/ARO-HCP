// Copyright 2025 Microsoft Corporation
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

package cleanup

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResourceDeletionOrder(t *testing.T) {
	// Document the deletion order - this serves as living documentation
	// The order matters due to Azure resource dependencies
	steps := []struct {
		step        string
		description string
		critical    bool
	}{
		{
			step:        "Step 1",
			description: "Network Security Perimeters (NSP) with force deletion",
			critical:    true,
		},
		{
			step:        "Step 2a",
			description: "Private DNS Zone Groups",
			critical:    true,
		},
		{
			step:        "Step 2b",
			description: "Private Endpoint Connections",
			critical:    true,
		},
		{
			step:        "Step 2c",
			description: "Private Endpoints",
			critical:    true,
		},
		{
			step:        "Step 2d",
			description: "Private DNS Zone Virtual Network Links",
			critical:    true,
		},
		{
			step:        "Step 2e",
			description: "Private Link Services",
			critical:    true,
		},
		{
			step:        "Step 2f",
			description: "Private DNS Zones",
			critical:    true,
		},
		{
			step:        "Step 3",
			description: "Public DNS Zones",
			critical:    false,
		},
		{
			step:        "Step 4",
			description: "Application Resources (bulk deletion - AKS, Cosmos, etc.)",
			critical:    false,
		},
		{
			step:        "Step 4b",
			description: "Public IP Addresses (with 3 retries after AKS deletion)",
			critical:    true,
		},
		{
			step:        "Step 5",
			description: "Monitoring Resources (Data Collection Rules & Endpoints)",
			critical:    false,
		},
		{
			step:        "Step 6",
			description: "Core Networking (Virtual Networks, NSGs)",
			critical:    true,
		},
		{
			step:        "Step 7",
			description: "Key Vault Purge (soft-deleted vaults)",
			critical:    false,
		},
		{
			step:        "Step 8",
			description: "Resource Group Deletion (with 5 retries)",
			critical:    true,
		},
	}

	assert.Equal(t, 14, len(steps), "Deletion process should have 14 steps")

	t.Log("Documented deletion order:")
	for _, step := range steps {
		criticalMarker := ""
		if step.critical {
			criticalMarker = " [CRITICAL ORDER]"
		}
		t.Logf("  %s: %s%s", step.step, step.description, criticalMarker)
	}
}

type fakeRetiredRoleAssignments struct {
	calls *[]string
	err   error
}

func (f fakeRetiredRoleAssignments) Cleanup(context.Context) error {
	*f.calls = append(*f.calls, "roles")
	return f.err
}

func TestWithRoleAssignmentRetirement(t *testing.T) {
	failure := errors.New("failed")
	for _, test := range []struct {
		name                                string
		enabled, dryRun                     bool
		captureErr, teardownErr, cleanupErr error
		wantCalls                           []string
		wantError                           bool
	}{
		{"opt in", true, false, nil, nil, nil, []string{"capture", "teardown", "roles"}, false},
		{"default", false, false, nil, nil, nil, []string{"teardown"}, false},
		{"dry run", true, true, nil, nil, nil, []string{"capture", "teardown"}, false},
		{"capture failure stops teardown", true, false, failure, nil, nil, []string{"capture"}, true},
		{"teardown failure preserves roles", true, false, nil, failure, nil, []string{"capture", "teardown"}, true},
		{"retirement failure is reported", true, false, nil, nil, failure, []string{"capture", "teardown", "roles"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			var capture func(context.Context) (retiredRoleAssignments, error)
			if test.enabled {
				capture = func(context.Context) (retiredRoleAssignments, error) {
					calls = append(calls, "capture")
					return fakeRetiredRoleAssignments{&calls, test.cleanupErr}, test.captureErr
				}
			}
			err := withRoleAssignmentRetirement(context.Background(), test.dryRun, capture, func(context.Context) error {
				calls = append(calls, "teardown")
				return test.teardownErr
			})
			if (err != nil) != test.wantError || !reflect.DeepEqual(calls, test.wantCalls) {
				t.Fatalf("error=%v calls=%v, want error=%t calls=%v", err, calls, test.wantError, test.wantCalls)
			}
		})
	}
}

func TestValidateRoleAssignmentRetirement(t *testing.T) {
	for _, test := range []struct {
		name      string
		enabled   bool
		cloud     string
		wait      bool
		wantError bool
	}{
		{"default remains unchanged", false, "public", false, false},
		{"explicit synchronous dev teardown", true, "dev", true, false},
		{"public rejected", true, "public", true, true},
		{"fairfax rejected", true, "fairfax", true, true},
		{"unknown cloud rejected", true, "", true, true},
		{"async rejected", true, "dev", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateRoleAssignmentRetirement(test.enabled, test.cloud, test.wait); (err != nil) != test.wantError {
				t.Fatalf("validation error=%v, want error=%t", err, test.wantError)
			}
		})
	}
}
