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

package azureutil

import (
	"net/http"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

func TestParsePrincipalID(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "hyphenated", value: "00000000-0000-0000-0000-0000000000ab"},
		{name: "uppercase", value: "00000000-0000-0000-0000-0000000000AB"},
		{name: "not a uuid", value: "not-a-uuid", wantErr: "invalid UUID"},
		{name: "padded", value: " 00000000-0000-0000-0000-0000000000ab ", wantErr: "hyphenated UUID"},
		{name: "unhyphenated", value: "000000000000000000000000000000ab", wantErr: "hyphenated UUID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParsePrincipalID(tc.value)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestRequireIdentities(t *testing.T) {
	t.Parallel()
	expected := NormalizedNameSet([]string{"Admin-API", "frontend"})
	err := RequireIdentities("rg", expected, map[string]string{"admin-api": "Admin-API"})
	if err == nil || !strings.Contains(err.Error(), "missing=[frontend]") {
		t.Fatalf("expected missing frontend, got %v", err)
	}
	if err := RequireIdentities("rg", expected, map[string]string{"admin-api": "Admin-API", "frontend": "frontend"}); err != nil {
		t.Fatal(err)
	}
	if got := sets.List(expected.Difference(sets.KeySet(map[string]string{"admin-api": "Admin-API"}))); len(got) != 1 || got[0] != "frontend" {
		t.Fatalf("unexpected difference %v", got)
	}
}

func TestIsNotFound(t *testing.T) {
	t.Parallel()
	if IsNotFound(nil) {
		t.Fatal("nil is not a 404")
	}
	if !IsNotFound(&azcore.ResponseError{StatusCode: http.StatusNotFound}) {
		t.Fatal("404 must match")
	}
	if IsNotFound(&azcore.ResponseError{StatusCode: http.StatusForbidden}) {
		t.Fatal("403 must not match")
	}
}

func TestIdentityNameDiff(t *testing.T) {
	t.Parallel()
	expected := NormalizedNameSet([]string{"Admin-API", "frontend"})
	missing, unexpected := IdentityNameDiff(expected, map[string]string{"admin-api": "Admin-API", "extra": "Extra"})
	if len(missing) != 1 || missing[0] != "frontend" {
		t.Fatalf("expected missing frontend, got %v", missing)
	}
	if len(unexpected) != 1 || unexpected[0] != "Extra" {
		t.Fatalf("expected unexpected Extra, got %v", unexpected)
	}
}

func TestUniqueReportedNames(t *testing.T) {
	t.Parallel()
	reported, err := UniqueReportedNames("rg", []string{"Admin-API", "frontend"})
	if err != nil {
		t.Fatal(err)
	}
	if reported["admin-api"] != "Admin-API" {
		t.Fatalf("unexpected reported names %v", reported)
	}
	if _, err := UniqueReportedNames("rg", []string{"admin-api", "Admin-API"}); err == nil || !strings.Contains(err.Error(), "duplicate identity") {
		t.Fatalf("expected duplicate identity, got %v", err)
	}
}

func TestClusterProfileCredentialRequiresProfileDir(t *testing.T) {
	t.Parallel()
	if _, err := ClusterProfileCredential(" "); err == nil || !strings.Contains(err.Error(), "selected cluster profile dir is empty") {
		t.Fatalf("expected empty profile dir error, got %v", err)
	}
}
