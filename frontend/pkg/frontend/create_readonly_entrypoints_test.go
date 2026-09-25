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

package frontend

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestSubscriptionReadOnlyMetadataHTTP(t *testing.T) {
	db := corecosmosstoragetesting.NewMockResourcesDBClient()
	reg := prometheus.NewRegistry()
	f := NewFrontend(testr.New(t), nil, nil, reg, reg, db, nil, newNoopAuditClient(t), coreapitesting.TestLocation, true)
	ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
	ts := newHTTPServer(ctx, f, db, nil)
	t.Cleanup(ts.Close)

	body := map[string]any{
		"cosmosMetadata": map[string]any{
			"resourceID":      "/subscriptions/11111111-2222-4333-8444-555555555555",
			"partitionKey":    "injected-partition",
			"etag":            "injected-etag",
			"instanceVersion": 999,
		},
		"state":            "Registered",
		"registrationDate": "Mon, 21 Sep 2026 12:00:00 GMT",
		"properties": map[string]any{
			"tenantId":            "11111111-2222-4333-8444-555555555555",
			"locationPlacementId": "Public_2014-09-01",
			"quotaId":             "EnterpriseAgreement_2014-09-01",
			"registeredFeatures": []any{
				map[string]any{"name": "Microsoft.RedHatOpenShift/TestFeature", "state": "Registered"},
			},
			"managedByTenants":     []any{map[string]any{"tenantId": "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"}},
			"additionalProperties": map[string]any{"keep": "value"},
		},
	}

	var previous *coreapi.Subscription
	for _, step := range []string{"create", "replace"} {
		t.Run(step, func(t *testing.T) {
			if step == "replace" {
				require.NotNil(t, previous, "create must have succeeded")
				body["state"] = "Warned" // Force Replace, rather than the unchanged-document shortcut.
			}
			encoded, err := json.Marshal(body)
			require.NoError(t, err)
			var want coreapi.Subscription
			require.NoError(t, json.Unmarshal(encoded, &want))
			req, err := http.NewRequestWithContext(ctx, http.MethodPut, ts.URL+coreapitesting.TestSubscriptionResourceID+"?api-version="+coreapi.SubscriptionAPIVersion, bytes.NewReader(encoded))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			resp, err := ts.Client().Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			responseBody, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode, "%s", responseBody)

			stored, err := db.Subscriptions().Get(ctx, coreapitesting.TestSubscriptionID)
			require.NoError(t, err)
			require.Equal(t, coreapitesting.TestSubscriptionResourceID, stored.ResourceID.String())
			require.Equal(t, strings.ToLower(coreapitesting.TestSubscriptionID), stored.PartitionKey)
			require.NotEmpty(t, stored.CosmosETag)
			require.NotEqual(t, "injected-etag", string(stored.CosmosETag))
			if previous == nil {
				require.Equal(t, int64(1), stored.InstanceVersion)
			} else {
				// The mock enforces If-Match, so success requires the stored ETag,
				// not the injected one; the version must advance from storage too.
				require.NotEqual(t, previous.CosmosETag, stored.CosmosETag)
				require.Equal(t, previous.InstanceVersion+1, stored.InstanceVersion)
			}
			require.Equal(t, want.State, stored.State)
			require.Equal(t, want.RegistrationDate, stored.RegistrationDate)
			require.Equal(t, want.Properties, stored.Properties)
			storedJSON, err := json.Marshal(stored)
			require.NoError(t, err)
			require.JSONEq(t, string(storedJSON), string(responseBody))
			previous = stored
		})
	}
}

func TestPreflightReadOnlyParityHTTP(t *testing.T) {
	db := corecosmosstoragetesting.NewMockResourcesDBClient()
	reg := prometheus.NewRegistry()
	f := NewFrontend(testr.New(t), nil, nil, reg, reg, db, nil, newNoopAuditClient(t), coreapitesting.TestLocation, true)
	ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
	ts := newHTTPServer(ctx, f, db, map[string]*coreapi.Subscription{
		coreapitesting.TestSubscriptionID: newTestSubscription(coreapitesting.TestSubscriptionID, coreapi.SubscriptionStateRegistered, nil),
	})
	t.Cleanup(ts.Close)

	for _, c := range readonlyCreateCases(t) {
		t.Run(c.version.String()+"/"+c.operation, func(t *testing.T) {
			// Maximum Swagger examples are not necessarily admission-valid. Keep
			// one deliberate writable error and require it in BOTH responses so
			// best-effort skipping cannot masquerade as read-only parity.
			body := c.schema.writable(t, c.body)
			var target, message string
			switch c.operation {
			case "HcpOpenShiftClusters_CreateOrUpdate":
				body = c.schema.inject(body, []string{"properties", "api", "visibility"}, "invisible")
				target, message = "properties.api.visibility", `Unsupported value: "invisible"`
			case "NodePools_CreateOrUpdate":
				body = c.schema.inject(body, []string{"properties", "platform", "osDisk", "sizeGiB"}, float64(-1))
				target, message = "properties.platform.osDisk.sizeGiB", "Invalid value: -1: must be greater than or equal to 64"
			case "ExternalAuths_CreateOrUpdate":
				body = c.schema.inject(body, []string{"properties", "issuer", "url"}, "http://issuer.example.com")
				target, message = "properties.issuer.url", "must be https URL"
			}
			for _, boundary := range c.boundaries {
				body = c.schema.inject(body, boundary.path, boundary.schema.sample(t, 1))
			}
			baseline := c.schema.writable(t, body).(map[string]any)
			injected := body.(map[string]any)
			var responses []coreapi.DeploymentPreflightResponse
			for _, resource := range []map[string]any{baseline, injected} {
				// Name and type are routing inputs in the preflight envelope.
				resource["name"] = c.resourceID.Name
				resource["type"] = c.resourceID.ResourceType.String()
				resource["apiVersion"] = c.version.String()
				encoded, err := json.Marshal(map[string]any{"resources": []any{resource}})
				require.NoError(t, err)
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+coreapitesting.TestDeploymentResourceID+"/preflight", bytes.NewReader(encoded))
				require.NoError(t, err)
				req.Header.Set("Content-Type", "application/json")
				resp, err := ts.Client().Do(req)
				require.NoError(t, err)
				defer resp.Body.Close()
				responseBody, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, resp.StatusCode, "%s", responseBody)
				var result coreapi.DeploymentPreflightResponse
				require.NoError(t, json.Unmarshal(responseBody, &result))
				if c.operation == "ExternalAuths_CreateOrUpdate" && result.Status == coreapi.DeploymentPreflightStatusSucceeded {
					// The envelope currently emits location even for proxy resources,
					// which strict external-auth decoding rejects before validation.
					resourceJSON, err := json.Marshal(resource)
					require.NoError(t, err)
					var envelope coreapi.DeploymentPreflightResource
					require.NoError(t, json.Unmarshal(resourceJSON, &envelope))
					require.ErrorContains(t, envelope.Convert(c.version.NewExternalAuth(nil)), `unknown field "location"`)
					require.Nil(t, result.Error)
					responses = append(responses, result)
					continue
				}
				require.Equal(t, coreapi.DeploymentPreflightStatusFailed, result.Status, "%s", responseBody)
				require.NotNil(t, result.Error)
				found := false
				for _, detail := range result.Error.Details {
					if detail.Target == target && strings.Contains(detail.Message, message) {
						found = true
					}
				}
				require.True(t, found, "expected %s error containing %q, got %s", target, message, responseBody)
				responses = append(responses, result)
			}
			// This checks observable validation parity, not every cleanup call:
			// most output fields are ignored by validation, and ensureSystemData
			// repairs required systemData fields even without read-only clearing.
			require.Equal(t, responses[0], responses[1])
			if responses[0].Status == coreapi.DeploymentPreflightStatusSucceeded {
				t.Skip("external-auth preflight skips strict decoding because its envelope includes location; writable validation and cleanup are not exercised")
			}
		})
	}
}
