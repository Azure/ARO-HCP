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
	"strings"
	"testing"
)

func TestParseEnvConfig(t *testing.T) {
	t.Run("missing required", func(t *testing.T) {
		_, err := parseEnvConfig(func(k string) string {
			if k == "ACR_NAME" {
				return "myacr"
			}
			return ""
		})
		if err == nil {
			t.Fatalf("expected error for missing required vars")
		}
		for _, want := range []string{"SUBSCRIPTION_ID", "RESOURCE_GROUP", "REPLICATION_REGION"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q should mention %q", err.Error(), want)
			}
		}
	})

	t.Run("complete config with no disabled regions", func(t *testing.T) {
		env := map[string]string{
			"SUBSCRIPTION_ID":    "sub",
			"RESOURCE_GROUP":     "rg",
			"ACR_NAME":           "myacr",
			"REPLICATION_REGION": "eastus2",
		}
		c, err := parseEnvConfig(func(k string) string { return env[k] })
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.subscriptionID != "sub" || c.resourceGroup != "rg" || c.acrName != "myacr" || c.region != "eastus2" {
			t.Fatalf("unexpected config: %+v", c)
		}
		if !c.desiredEndpointEnabled() {
			t.Fatalf("expected endpoint enabled by default")
		}
	})

	t.Run("REPLICATION_STATE false disables endpoint", func(t *testing.T) {
		env := map[string]string{
			"SUBSCRIPTION_ID":    "sub",
			"RESOURCE_GROUP":     "rg",
			"ACR_NAME":           "myacr",
			"REPLICATION_REGION": "eastus2euap",
			"REPLICATION_STATE":  "false",
		}
		c, err := parseEnvConfig(func(k string) string { return env[k] })
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.desiredEndpointEnabled() {
			t.Fatalf("expected endpoint disabled for eastus2euap")
		}
	})

	t.Run("REPLICATION_STATE true enables endpoint", func(t *testing.T) {
		env := map[string]string{
			"SUBSCRIPTION_ID":    "sub",
			"RESOURCE_GROUP":     "rg",
			"ACR_NAME":           "myacr",
			"REPLICATION_REGION": "eastus2",
			"REPLICATION_STATE":  "true",
		}
		c, err := parseEnvConfig(func(k string) string { return env[k] })
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !c.desiredEndpointEnabled() {
			t.Fatalf("expected endpoint enabled for eastus2")
		}
	})

	t.Run("invalid REPLICATION_STATE is an error", func(t *testing.T) {
		env := map[string]string{
			"SUBSCRIPTION_ID":    "sub",
			"RESOURCE_GROUP":     "rg",
			"ACR_NAME":           "myacr",
			"REPLICATION_REGION": "eastus2",
			"REPLICATION_STATE":  "maybe",
		}
		_, err := parseEnvConfig(func(k string) string { return env[k] })
		if err == nil {
			t.Fatalf("expected error for invalid REPLICATION_STATE")
		}
	})
}

func TestIsNotFound(t *testing.T) {
	if isNotFound(nil) {
		t.Fatalf("nil error should not be not-found")
	}
	if isNotFound(errPlain("boom")) {
		t.Fatalf("plain error should not be not-found")
	}
}

// errPlain is a minimal error type for TestIsNotFound, distinct from
// *azcore.ResponseError so errors.As cannot match it.
type errPlain string

func (e errPlain) Error() string { return string(e) }
