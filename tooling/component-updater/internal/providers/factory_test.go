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

package providers

import (
	"errors"
	"strings"
	"testing"
)

func TestRegistryRejectsUnknownProviders(t *testing.T) {
	if _, err := New("azure-grafna", Deps{}); err == nil || !strings.Contains(err.Error(), "supported: azure-istio") {
		t.Fatalf("unknown provider must fail and list supported providers: %v", err)
	}
	if err := Validate("azure-grafna", ComponentSpec{}); err == nil || !strings.Contains(err.Error(), "unsupported provider") {
		t.Fatalf("unknown provider must fail validation: %v", err)
	}
}

func TestAzureAccessIsRequestedOnlyByAzureProviders(t *testing.T) {
	calls := 0
	deps := Deps{Azure: func() (AzureAccess, error) {
		calls++
		return AzureAccess{}, errors.New("no azure")
	}}
	if _, err := New(ProviderAzureIstio, deps); err == nil || calls != 1 {
		t.Fatalf("azure-istio must obtain Azure access through Deps: calls=%d, err=%v", calls, err)
	}
	if _, err := New(ProviderAzureIstio, Deps{}); err == nil || !strings.Contains(err.Error(), "azure access is not configured") {
		t.Fatalf("missing Azure access must fail clearly: %v", err)
	}
}
