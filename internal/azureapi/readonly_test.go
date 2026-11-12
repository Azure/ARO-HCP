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

package azureapi_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/azureapi/v20240610preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20251223preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20260630preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20260901preview"
	v20261001 "github.com/Azure/ARO-HCP/internal/azureapi/v20261001"
)

func TestClearReadOnlyFields(t *testing.T) {
	type resource interface {
		NewExternal() any
		ClearReadOnlyFields()
	}
	versions := map[string][]resource{
		"2024-06-10-preview": {(*v20240610preview.HcpOpenShiftCluster)(nil), (*v20240610preview.NodePool)(nil), (*v20240610preview.ExternalAuth)(nil)},
		"2025-12-23-preview": {(*v20251223preview.HcpOpenShiftCluster)(nil), (*v20251223preview.NodePool)(nil), (*v20251223preview.ExternalAuth)(nil)},
		"2026-06-30-preview": {(*v20260630preview.HcpOpenShiftCluster)(nil), (*v20260630preview.NodePool)(nil), (*v20260630preview.ExternalAuth)(nil)},
		"2026-09-01-preview": {(*v20260901preview.HcpOpenShiftCluster)(nil), (*v20260901preview.NodePool)(nil), (*v20260901preview.ExternalAuth)(nil)},
		"2026-10-01":         {(*v20261001.HcpOpenShiftCluster)(nil), (*v20261001.NodePool)(nil), (*v20261001.ExternalAuth)(nil)},
	}
	cases := []struct {
		name       string
		properties string
		want       string
	}{
		{
			name: "cluster",
			properties: `{
				"provisioningState":"Succeeded",
				"console":{"url":"https://console.example.com"},
				"dns":{"baseDomain":"example.com","baseDomainPrefix":"keep"},
				"api":{"url":"https://api.example.com","visibility":"Private"},
				"platform":{"issuerUrl":"https://issuer.example.com","managedResourceGroup":"keep","subnetId":"keep"},
				"clusterImageRegistry":{"state":"Disabled"},
				"version":{"id":"4.20","channelGroup":"stable"},
				"network":{"hostPrefix":0},"nodeDrainTimeoutMinutes":0
			}`,
			want: `{
				"dns":{"baseDomainPrefix":"keep"},
				"api":{"visibility":"Private"},
				"platform":{"managedResourceGroup":"keep","subnetId":"keep"},
				"clusterImageRegistry":{"state":"Disabled"},
				"version":{"id":"4.20","channelGroup":"stable"},
				"network":{"hostPrefix":0},"nodeDrainTimeoutMinutes":0
			}`,
		},
		{
			name: "nodepool",
			properties: `{
				"provisioningState":"Succeeded",
				"platform":{"vmSize":"keep","subnetId":"keep","availabilityZone":"1"},
				"autoRepair":false,"replicas":0,"nodeDrainTimeoutMinutes":0,
				"labels":[null,{"key":"keep","value":"keep"}],"taints":[],
				"version":{"id":"4.20.1","channelGroup":"stable"}
			}`,
			want: `{
				"platform":{"vmSize":"keep","subnetId":"keep","availabilityZone":"1"},
				"autoRepair":false,"replicas":0,"nodeDrainTimeoutMinutes":0,
				"labels":[null,{"key":"keep","value":"keep"}],"taints":[],
				"version":{"id":"4.20.1","channelGroup":"stable"}
			}`,
		},
		{
			name: "externalauth",
			properties: `{
				"provisioningState":"Succeeded",
				"issuer":{"url":"https://keep.example.com","audiences":[null,"keep"]},
				"claim":{"mappings":{"username":{"claim":"keep"}}},
				"clients":[null,{"clientId":"keep","component":{"name":"keep","authClientNamespace":"keep"},"extraScopes":[]}]
			}`,
			want: `{
				"issuer":{"url":"https://keep.example.com","audiences":[null,"keep"]},
				"claim":{"mappings":{"username":{"claim":"keep"}}},
				"clients":[null,{"clientId":"keep","component":{"name":"keep","authClientNamespace":"keep"},"extraScopes":[]}]
			}`,
		},
	}
	for version, resources := range versions {
		for i, prototype := range resources {
			t.Run(version+"/"+cases[i].name, func(t *testing.T) {
				// Typed nil receivers and omitted optional containers must be safe.
				prototype.ClearReadOnlyFields()
				bodies := []string{`{}`, `{"properties":null}`, `{"properties":{}}`}
				if i != 2 {
					bodies = append(bodies,
						`{"properties":{"platform":{}},"identity":null}`,
						`{"identity":{}}`,
						`{"identity":{"userAssignedIdentities":{}}}`)
				}
				for _, body := range bodies {
					got := prototype.NewExternal().(resource)
					want := prototype.NewExternal()
					require.NoError(t, json.Unmarshal([]byte(body), got))
					require.NoError(t, json.Unmarshal([]byte(body), want))
					got.ClearReadOnlyFields()
					require.Equal(t, want, got, "preserve nil and empty writable containers: %s", body)
				}

				for _, emptyReadOnly := range []bool{false, true} {
					properties := cases[i].properties
					wantProperties := cases[i].want
					status := `{"conditions":[null,{"message":"discard"}]}`
					if version == "2026-10-01" && i != 2 {
						status = `{"conditions":[null,{"message":"discard"}],"activeVersions":[{"version":"4.20"}]}`
					}
					if emptyReadOnly {
						properties, wantProperties = `{}`, `{}`
						status = `{}`
						if i == 0 {
							properties = `{"console":{},"dns":{},"api":{},"platform":{}}`
							wantProperties = `{"dns":{},"api":{},"platform":{}}`
						}
					}
					var propertyFields map[string]json.RawMessage
					require.NoError(t, json.Unmarshal([]byte(properties), &propertyFields))
					if version >= "2026-06-30-preview" {
						propertyFields["status"] = json.RawMessage(status)
					} else if i == 2 {
						condition := `{"message":"discard"}`
						if emptyReadOnly {
							condition = `{}`
						}
						propertyFields["condition"] = json.RawMessage(condition)
					}
					propertyJSON, err := json.Marshal(propertyFields)
					require.NoError(t, err)
					tracked := `"location":"westus3","tags":{"keep":"value","nil":null},
						"identity":{"type":"UserAssigned","principalId":"discard","tenantId":"discard",
							"userAssignedIdentities":{"keep":{"clientId":"discard","principalId":"discard"},"empty":{},"nil":null}},`
					wantTracked := `"location":"westus3","tags":{"keep":"value","nil":null},
						"identity":{"type":"UserAssigned","userAssignedIdentities":{"keep":{},"empty":{},"nil":null}},`
					if i == 2 {
						tracked, wantTracked = "", ""
					}
					body := `{
						"id":"discard even malformed IDs","type":"discard","systemData":{"createdBy":"discard"},
						"name":"keep",` + tracked + `"properties":` + string(propertyJSON) + `}`
					wantBody := `{
						"name":"keep",` + wantTracked + `"properties":` + wantProperties + `}`
					got := prototype.NewExternal().(resource)
					want := prototype.NewExternal()
					require.NoError(t, json.Unmarshal([]byte(body), got))
					require.NoError(t, json.Unmarshal([]byte(wantBody), want))
					require.NotEqual(t, want, got, "read-only fields must be populated before clearing")
					got.ClearReadOnlyFields()
					// Compare typed values directly: conversion or marshaling can drop read-only fields.
					require.Equal(t, want, got, "clear read-only fields without changing writable fields")
					got.ClearReadOnlyFields()
					require.Equal(t, want, got, "clearing must be idempotent")
				}
			})
		}
	}
}
