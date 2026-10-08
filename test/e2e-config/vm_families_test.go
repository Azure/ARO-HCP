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

package e2econfig

import (
	"reflect"
	"testing"
)

func TestVMFamilyConfigPrecedence(t *testing.T) {
	config, err := ParseVMFamilyConfig([]byte(`version: 1
environments:
  ci01:
    defaults:
      worker_families: [first, second]
      helper_families: [helper]
    regions:
      WestUS3:
        worker_families: [regional]
      uksouth:
        helper_families: [regional-helper, fallback-helper]
  int:
    regions:
      westus3:
        worker_families: [int-worker]
`))
	if err != nil {
		t.Fatalf("expected valid defaults and overrides: %v", err)
	}
	for _, tt := range []struct {
		env, region string
		want        VMFamilyPolicy
	}{
		{"ci01", "WESTUS3", VMFamilyPolicy{WorkerFamilies: []string{"regional"}, HelperFamilies: []string{"helper"}}},
		{"ci01", "uksouth", VMFamilyPolicy{WorkerFamilies: []string{"first", "second"}, HelperFamilies: []string{"regional-helper", "fallback-helper"}}},
		{"ci01", "centralus", VMFamilyPolicy{WorkerFamilies: []string{"first", "second"}, HelperFamilies: []string{"helper"}}},
		{"int", "westus3", VMFamilyPolicy{WorkerFamilies: []string{"int-worker"}}},
		{"int", "uksouth", VMFamilyPolicy{}},
		{"prod", "westus3", VMFamilyPolicy{}},
	} {
		t.Run(tt.env+"/"+tt.region, func(t *testing.T) {
			got := config.Resolve(tt.env, tt.region)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("expected per-role replacement/inheritance %+v, got %+v", tt.want, got)
			}
		})
	}
}

func TestVMFamilyConfigRejectsMalformedPolicies(t *testing.T) {
	for name, data := range map[string]string{
		"missing version":                "environments: {}",
		"unsupported version":            "version: 2\nenvironments: {}",
		"missing environments":           "version: 1",
		"unknown field":                  "version: 1\nenvironments: {}\nworkers: []",
		"unknown policy field":           "version: 1\nenvironments: {int: {defaults: {workers: [family]}}}",
		"empty default list":             "version: 1\nenvironments: {int: {defaults: {worker_families: []}}}",
		"empty override list":            "version: 1\nenvironments: {int: {regions: {westus3: {helper_families: []}}}}",
		"null override":                  "version: 1\nenvironments: {int: {regions: {westus3: {worker_families: null}}}}",
		"empty family":                   "version: 1\nenvironments: {int: {defaults: {worker_families: ['']}}}",
		"duplicate family":               "version: 1\nenvironments: {int: {defaults: {worker_families: [family, family]}}}",
		"duplicate region ignoring case": "version: 1\nenvironments: {int: {regions: {westus3: {}, WestUS3: {}}}}",
		"multiple documents":             "version: 1\nenvironments: {}\n---\nversion: 1\nenvironments: {}",
		"invalid YAML":                   "[",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseVMFamilyConfig([]byte(data)); err == nil {
				t.Fatal("expected invalid family policy to fail clearly")
			}
		})
	}
}
