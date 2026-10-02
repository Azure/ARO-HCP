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
	"reflect"
	"testing"
)

func TestIngressCertificateVaultURLs(test *testing.T) {
	for _, scenario := range []struct {
		name  string
		value string
		want  []string
	}{
		{name: "missing"},
		{name: "HTTP", value: "http://example.vault.azure.net"},
		{name: "path", value: "https://example.vault.azure.net/certificates"},
		{name: "userinfo", value: "https://user@example.vault.azure.net"},
		{name: "query", value: "https://example.vault.azure.net?query=yes"},
		{name: "empty entry", value: "https://example.vault.azure.net,"},
		{name: "multiple", value: " https://first.vault.azure.net/, https://second.vault.azure.net ", want: []string{"https://first.vault.azure.net", "https://second.vault.azure.net"}},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			test.Setenv("ARO_HCP_INGRESS_CERTIFICATE_VAULT_URLS", scenario.value)
			actual, err := IngressCertificateVaultURLs()
			if scenario.want == nil {
				if err == nil {
					test.Fatal("expected invalid or missing vault configuration to fail")
				}
				return
			}
			if err != nil || !reflect.DeepEqual(actual, scenario.want) {
				test.Fatalf("vault URLs = %v, error = %v; want %v", actual, err, scenario.want)
			}
		})
	}
}
