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
	"encoding/json"
	"reflect"
	"testing"
)

func TestParseVMFamilyPolicy(t *testing.T) {
	for _, tt := range []struct {
		name string
		data string
		want VMFamilyPolicy
	}{
		{"unconfigured", `{}`, VMFamilyPolicy{}},
		{"worker only", `{"worker_families":["first","second"]}`, VMFamilyPolicy{WorkerFamilies: []string{"first", "second"}}},
		{"helper only", `{"helper_families":["helper"]}`, VMFamilyPolicy{HelperFamilies: []string{"helper"}}},
		{"both roles", `{"worker_families":["first","second"],"helper_families":["helper","first"]}`, VMFamilyPolicy{WorkerFamilies: []string{"first", "second"}, HelperFamilies: []string{"helper", "first"}}},
		{"surrounding whitespace", " \n {\"worker_families\":[\"family\"]} \n ", VMFamilyPolicy{WorkerFamilies: []string{"family"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseVMFamilyPolicy([]byte(tt.data))
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("expected ordered policy %+v, got %+v err=%v", tt.want, got, err)
			}
			data, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("failed to serialize resolved policy: %v", err)
			}
			roundTrip, err := ParseVMFamilyPolicy(data)
			if err != nil || !reflect.DeepEqual(roundTrip, tt.want) {
				t.Fatalf("expected omitted roles and order to survive JSON round trip, got %+v err=%v", roundTrip, err)
			}
		})
	}
}

func TestParseVMFamilyPolicyRejectsMalformedPolicies(t *testing.T) {
	for name, data := range map[string]string{
		"empty override":         "",
		"whitespace override":    " \n ",
		"null object":            `null`,
		"array object":           `[]`,
		"string object":          `"policy"`,
		"number object":          `42`,
		"unknown key":            `{"workers":["family"]}`,
		"case changed key":       `{"Worker_Families":["family"]}`,
		"region resolution":      `{"regions":{}}`,
		"old environment config": `{"version":1,"environments":{}}`,
		"duplicate worker key":   `{"worker_families":["first"],"worker_families":["second"]}`,
		"duplicate helper key":   `{"helper_families":["first"],"helper_families":["second"]}`,
		"escaped duplicate key":  `{"worker_families":["first"],"worker\u005ffamilies":["second"]}`,
		"empty worker list":      `{"worker_families":[]}`,
		"empty helper list":      `{"helper_families":[]}`,
		"null worker list":       `{"worker_families":null}`,
		"null helper list":       `{"helper_families":null}`,
		"string role":            `{"worker_families":"family"}`,
		"object role":            `{"worker_families":{}}`,
		"empty family":           `{"worker_families":[""]}`,
		"null family":            `{"worker_families":[null]}`,
		"numeric family":         `{"worker_families":[42]}`,
		"whitespace family":      `{"worker_families":[" family"]}`,
		"trailing whitespace":    `{"helper_families":["family\t"]}`,
		"duplicate family":       `{"worker_families":["family","family"]}`,
		"multiple documents":     `{} {}`,
		"trailing null document": `{} null`,
		"trailing garbage":       `{} garbage`,
		"truncated object":       `{"worker_families":["family"]`,
		"invalid closing token":  `{"worker_families":["family"]]`,
		"trailing comma":         `{"worker_families":["family"],}`,
		"YAML override":          "worker_families: [family]",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseVMFamilyPolicy([]byte(data)); err == nil {
				t.Fatal("expected invalid family policy to fail clearly")
			}
		})
	}
}

func TestVMFamilyPolicyValidate(t *testing.T) {
	for _, tt := range []struct {
		name    string
		policy  VMFamilyPolicy
		wantErr bool
	}{
		{"nil roles", VMFamilyPolicy{}, false},
		{"ordered families", VMFamilyPolicy{WorkerFamilies: []string{"second", "first"}}, false},
		{"same family across roles", VMFamilyPolicy{WorkerFamilies: []string{"family"}, HelperFamilies: []string{"family"}}, false},
		{"empty workers", VMFamilyPolicy{WorkerFamilies: []string{}}, true},
		{"empty helpers", VMFamilyPolicy{HelperFamilies: []string{}}, true},
		{"empty family", VMFamilyPolicy{WorkerFamilies: []string{""}}, true},
		{"whitespace family", VMFamilyPolicy{HelperFamilies: []string{"family "}}, true},
		{"duplicate family", VMFamilyPolicy{HelperFamilies: []string{"family", "family"}}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.policy.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("expected validation error=%t, got %v", tt.wantErr, err)
			}
		})
	}
}
