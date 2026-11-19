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

package framework

import (
	"testing"

	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
)

// TestIsProdDeployEnvironment pins the guard used by specs that must not
// execute outside production. Only an explicit production value may proceed:
// anything else, including an unset variable, has to report false, because a
// run that cannot prove it is production must not perform the validation.
func TestIsProdDeployEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		set   bool
		want  bool
	}{
		{name: "prod", value: "prod", set: true, want: true},
		{name: "prod with surrounding whitespace", value: "  prod  ", set: true, want: true},
		{name: "prod uppercase", value: "PROD", set: true, want: true},

		{name: "stage", value: "stg", set: true, want: false},
		{name: "stage spelled out", value: "stage", set: true, want: false},
		{name: "integration", value: "int", set: true, want: false},
		{name: "development", value: "dev", set: true, want: false},
		{name: "ci slot", value: "ci01", set: true, want: false},

		{name: "unset", set: false, want: false},
		{name: "empty", value: "", set: true, want: false},
		{name: "whitespace only", value: "   ", set: true, want: false},

		// Guard against a prefix or substring match quietly admitting a
		// non-production environment.
		{name: "prefixed", value: "notprod", set: true, want: false},
		{name: "suffixed", value: "prod-canary", set: true, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// t.Setenv both sets the variable and restores it afterwards; for
			// the unset case set it empty first so an inherited value from the
			// developer's shell cannot leak into the result.
			t.Setenv(leaseenv.RuntimeDeployEnvironment, "")
			if tc.set {
				t.Setenv(leaseenv.RuntimeDeployEnvironment, tc.value)
			}

			if got := IsProdDeployEnvironment(); got != tc.want {
				t.Errorf("IsProdDeployEnvironment() with %s=%q = %v, want %v",
					leaseenv.RuntimeDeployEnvironment, tc.value, got, tc.want)
			}
		})
	}
}
