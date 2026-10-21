// Copyright 2026 Microsoft Corporation
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

package configoverride

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
)

func validBundle() map[string]interface{} {
	return map[string]interface{}{
		"serviceResourceGroup":       "lease-service-rg",
		"serviceAksIdentityName":     "Pool_Service-aks",
		"managementResourceGroups":   map[string]interface{}{"2": "lease-two-rg", "1": "lease-one-rg"},
		"managementAksIdentityNames": map[string]interface{}{"1": "Pool_One-aks", "2": "Pool_Two-aks"},
	}
}

func bundleJSON(t *testing.T, bundle map[string]interface{}) string {
	t.Helper()
	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func rejectBundle(t *testing.T, payload string) {
	t.Helper()
	result, err := Generate(Inputs{DeployEnvironment: "ci01", InfrastructureIdentityBundle: payload}, Paths{})
	if err == nil || result.YAML != nil || result.OCLoginRegistries != "" {
		t.Fatalf("Generate(%s) = %#v, %v; want error without result", payload, result, err)
	}
}

func TestSharedIdentityBundleContract(t *testing.T) {
	bundle := leaseenv.InfrastructureIdentityBundlePayload{
		ServiceResourceGroup: "lease-service-rg", ServiceAksIdentityName: "Pool_Service-aks",
		ManagementResourceGroups:   map[string]string{"2": "lease-two-rg", "1": "lease-one-rg"},
		ManagementAksIdentityNames: map[string]string{"1": "Pool_One-aks", "2": "Pool_Two-aks"},
	}
	payload, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	input, err := InputsFromEnvironment([]string{
		leaseenv.DeployEnvironment + "=ci01",
		leaseenv.MSIContainers + "=leased",
		leaseenv.InfrastructureIdentityBundle + "=" + string(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, document := generated(t, input, Paths{})
	want := map[string]interface{}{"infrastructureIdentities": map[string]interface{}{
		"useLeased": true, "serviceResourceGroup": "lease-service-rg", "serviceAksIdentityName": "Pool_Service-aks",
		"managementResourceGroups":   "1=lease-one-rg,2=lease-two-rg",
		"managementAksIdentityNames": "1=Pool_One-aks,2=Pool_Two-aks",
	}}
	if got := defaultsAt(t, document, "ci01"); !reflect.DeepEqual(got, want) {
		t.Fatalf("typed bundle defaults = %#v, want %#v", got, want)
	}
}

func TestInfrastructureIdentityAcceptedPayloads(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		change                     func(map[string]interface{})
		wantGroups, wantIdentities string
	}{
		{name: "explicit stable names", wantGroups: "1=lease-one-rg,2=lease-two-rg", wantIdentities: "1=Pool_One-aks,2=Pool_Two-aks"},
		{
			name: "name boundaries",
			change: func(bundle map[string]interface{}) {
				bundle["serviceResourceGroup"] = "a"
				bundle["serviceAksIdentityName"] = "A01"
				bundle["managementResourceGroups"] = map[string]interface{}{"1": strings.Repeat("a", 90), "2": ".group_with-(valid).characters"}
				bundle["managementAksIdentityNames"] = map[string]interface{}{"1": strings.Repeat("A", 128), "2": "9_end-"}
			},
			wantGroups:     "1=" + strings.Repeat("a", 90) + ",2=.group_with-(valid).characters",
			wantIdentities: "1=" + strings.Repeat("A", 128) + ",2=9_end-",
		},
		{
			name: "numeric stamp order without integer overflow",
			change: func(bundle map[string]interface{}) {
				bundle["managementResourceGroups"] = map[string]interface{}{"10": "rg-ten", "2": "rg-two", "18446744073709551616": "rg-large", "18446744073709551617": "rg-larger"}
				bundle["managementAksIdentityNames"] = map[string]interface{}{"18446744073709551617": "aks-larger", "18446744073709551616": "aks-large", "2": "aks-two", "10": "aks-ten"}
			},
			wantGroups:     "2=rg-two,10=rg-ten,18446744073709551616=rg-large,18446744073709551617=rg-larger",
			wantIdentities: "2=aks-two,10=aks-ten,18446744073709551616=aks-large,18446744073709551617=aks-larger",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle := validBundle()
			if tc.change != nil {
				tc.change(bundle)
			}
			_, document := generated(t, Inputs{DeployEnvironment: "ci01", MSIContainers: "leased", InfrastructureIdentityBundle: bundleJSON(t, bundle)}, Paths{})
			want := map[string]interface{}{"infrastructureIdentities": map[string]interface{}{
				"useLeased": true, "serviceResourceGroup": bundle["serviceResourceGroup"], "serviceAksIdentityName": bundle["serviceAksIdentityName"],
				"managementResourceGroups": tc.wantGroups, "managementAksIdentityNames": tc.wantIdentities,
			}}
			if got := defaultsAt(t, document, "ci01"); !reflect.DeepEqual(got, want) {
				t.Fatalf("defaults = %#v, want %#v", got, want)
			}
		})
	}
}

func TestInfrastructureIdentityStrictFields(t *testing.T) {
	for _, field := range []string{"serviceResourceGroup", "serviceAksIdentityName", "managementResourceGroups", "managementAksIdentityNames"} {
		t.Run("missing "+field, func(t *testing.T) {
			bundle := validBundle()
			delete(bundle, field)
			rejectBundle(t, bundleJSON(t, bundle))
		})
		t.Run("wrong case "+field, func(t *testing.T) {
			bundle := validBundle()
			bundle[strings.ToUpper(field)] = bundle[field]
			delete(bundle, field)
			rejectBundle(t, bundleJSON(t, bundle))
		})
	}
	t.Run("unknown field", func(t *testing.T) {
		bundle := validBundle()
		bundle["unexpected"] = "not allowed"
		rejectBundle(t, bundleJSON(t, bundle))
	})
	for _, payload := range []string{"{", "null", "[]", `"string"`, "true", "42", "{}", " ", "{} {}"} {
		t.Run("invalid JSON object "+payload, func(t *testing.T) { rejectBundle(t, payload) })
	}
	for _, field := range []string{"managementResourceGroups", "managementAksIdentityNames"} {
		for _, value := range []interface{}{nil, map[string]interface{}{}, []interface{}{}, "1=not-an-object", 42, true} {
			t.Run(fmt.Sprintf("%s=%v", field, value), func(t *testing.T) {
				bundle := validBundle()
				bundle[field] = value
				rejectBundle(t, bundleJSON(t, bundle))
			})
		}
		for _, change := range []string{"missing", "extra", "mismatched"} {
			t.Run(field+" "+change+" stamp", func(t *testing.T) {
				bundle := validBundle()
				stamps := bundle[field].(map[string]interface{})
				if change != "extra" {
					delete(stamps, "2")
				}
				if change != "missing" {
					stamps["3"] = "extra-name"
				}
				rejectBundle(t, bundleJSON(t, bundle))
			})
		}
	}
	for _, stamp := range []string{"0", "01", "-1", "+1", "1.0", "1e1", " 1", "1 ", "1\n", "1,2", "1=2", "one", ""} {
		t.Run("invalid stamp="+stamp, func(t *testing.T) {
			bundle := validBundle()
			bundle["managementResourceGroups"] = map[string]interface{}{stamp: "valid-group"}
			bundle["managementAksIdentityNames"] = map[string]interface{}{stamp: "valid-identity"}
			rejectBundle(t, bundleJSON(t, bundle))
		})
	}
}

func TestInfrastructureIdentityNameBoundaries(t *testing.T) {
	for _, field := range []string{"serviceAksIdentityName", "managementAksIdentityNames", "serviceResourceGroup", "managementResourceGroups"} {
		identity := strings.Contains(field, "Identity")
		values := []interface{}{nil, 42, true, []interface{}{}, map[string]interface{}{}, "", "has space", "has/slash", "has,comma", "has=equals", "nonascii-é", "trailing\n", "has\ttab", "has\rcarriage"}
		if identity {
			values = append(values, "a", "ab", "-leading", "_leading", "has.dot", strings.Repeat("a", 129))
		} else {
			values = append(values, "trailing.", ".", "has:colon", strings.Repeat("a", 91))
		}
		for _, value := range values {
			t.Run(fmt.Sprintf("%s=%v", field, value), func(t *testing.T) {
				bundle := validBundle()
				if strings.HasPrefix(field, "management") {
					bundle[field].(map[string]interface{})["1"] = value
				} else {
					bundle[field] = value
				}
				rejectBundle(t, bundleJSON(t, bundle))
			})
		}
	}
}

func TestInfrastructureIdentityAbsent(t *testing.T) {
	for _, environment := range [][]string{{"DEPLOY_ENV=ci01"}, {"DEPLOY_ENV=ci01", "LEASED_INFRASTRUCTURE_IDENTITY_BUNDLE="}} {
		input, err := InputsFromEnvironment(environment)
		if err != nil {
			t.Fatal(err)
		}
		_, document := generated(t, input, Paths{})
		if _, ok := defaultsAt(t, document, "ci01")["infrastructureIdentities"]; ok {
			t.Fatal("absent bundle generated identity overrides")
		}
	}
}
