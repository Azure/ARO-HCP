// Copyright 2025 Microsoft Corporation
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

package alertmanagement

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertprocessingrules/armalertprocessingrules"
	armaprfake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertprocessingrules/armalertprocessingrules/fake"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

const (
	testResourceGroup = "rg-region"
	testSubscription  = "00000000-0000-0000-0000-000000000000"
	testAmw           = "amw-region"
	scopeFormat       = "/subscriptions/%s/resourceGroups/%s/providers/microsoft.monitor/accounts/%s"
)

var testScopes = []string{
	fmt.Sprintf(scopeFormat, testSubscription, testResourceGroup, testAmw),
}

// newFakeClient builds an armalertprocessingrules.Client whose transport is
// backed by the provided fake server.
func newFakeClient(t *testing.T, srv armaprfake.Server) *armalertprocessingrules.Client {
	t.Helper()
	client, err := armalertprocessingrules.NewClient(
		testSubscription,
		&azfake.TokenCredential{},
		&azcorearm.ClientOptions{
			ClientOptions: azcore.ClientOptions{
				Transport: armaprfake.NewServerTransport(&srv),
			},
		},
	)
	require.NoError(t, err)
	return client
}

func sampleRule(name string) *armalertprocessingrules.AlertProcessingRule {
	return &armalertprocessingrules.AlertProcessingRule{
		Name:     to.Ptr(name),
		ID:       to.Ptr("/subscriptions/" + testSubscription + "/resourceGroups/" + testResourceGroup + "/providers/Microsoft.AlertsManagement/actionRules/" + name),
		Location: to.Ptr("Global"),
		Tags:     map[string]*string{aroHCPPurposeTagKey: to.Ptr(aroHCPPurposeTagValue)},
		Properties: &armalertprocessingrules.AlertProcessingRuleProperties{
			Enabled:     to.Ptr(true),
			Description: to.Ptr("desc-" + name),
			Scopes:      to.SliceOfPtrs(testScopes...),
			Schedule: &armalertprocessingrules.Schedule{
				EffectiveFrom:  to.Ptr("2026-01-01T00:00:00"),
				EffectiveUntil: to.Ptr("2026-01-02T00:00:00"),
			},
		},
	}
}

func findCondition(conds []*armalertprocessingrules.Condition, field armalertprocessingrules.Field) *armalertprocessingrules.Condition {
	for _, c := range conds {
		if c.Field != nil && *c.Field == field {
			return c
		}
	}
	return nil
}

func TestAlertProcessingRuleListHandler(t *testing.T) {
	t.Parallel()

	t.Run("aggregates rules across pages", func(t *testing.T) {
		t.Parallel()
		srv := armaprfake.Server{
			NewListByResourceGroupPager: func(resourceGroupName string, _ *armalertprocessingrules.ClientListByResourceGroupOptions) azfake.PagerResponder[armalertprocessingrules.ClientListByResourceGroupResponse] {
				require.Equal(t, testResourceGroup, resourceGroupName)
				var resp azfake.PagerResponder[armalertprocessingrules.ClientListByResourceGroupResponse]
				resp.AddPage(http.StatusOK, armalertprocessingrules.ClientListByResourceGroupResponse{
					List: armalertprocessingrules.List{Value: []*armalertprocessingrules.AlertProcessingRule{sampleRule("rule-a")}},
				}, nil)
				resp.AddPage(http.StatusOK, armalertprocessingrules.ClientListByResourceGroupResponse{
					List: armalertprocessingrules.List{Value: []*armalertprocessingrules.AlertProcessingRule{sampleRule("rule-b")}},
				}, nil)
				return resp
			},
		}
		handler := NewAlertProcessingRuleListHandler(newFakeClient(t, srv), testResourceGroup)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/admin/v1/alertprocessingrules", nil)
		require.NoError(t, handler.ServeHTTP(rec, req))
		require.Equal(t, http.StatusOK, rec.Code)

		var resp AlertProcessingRuleListResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		require.Len(t, resp.Value, 2)
		require.Equal(t, "rule-a", resp.Value[0].Name)
		require.Equal(t, "rule-b", resp.Value[1].Name)
		require.True(t, resp.Value[0].Enabled)
		require.Equal(t, testScopes, resp.Value[0].Scopes)
		require.Equal(t, "2026-01-01T00:00:00", resp.Value[0].EffectiveFrom)
	})

	t.Run("empty list serializes as empty array", func(t *testing.T) {
		t.Parallel()
		srv := armaprfake.Server{
			NewListByResourceGroupPager: func(_ string, _ *armalertprocessingrules.ClientListByResourceGroupOptions) azfake.PagerResponder[armalertprocessingrules.ClientListByResourceGroupResponse] {
				var resp azfake.PagerResponder[armalertprocessingrules.ClientListByResourceGroupResponse]
				resp.AddPage(http.StatusOK, armalertprocessingrules.ClientListByResourceGroupResponse{}, nil)
				return resp
			},
		}
		handler := NewAlertProcessingRuleListHandler(newFakeClient(t, srv), testResourceGroup)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/admin/v1/alertprocessingrules", nil)
		require.NoError(t, handler.ServeHTTP(rec, req))
		require.JSONEq(t, `{"value":[]}`, rec.Body.String())
	})
}

func TestAlertProcessingRuleGetHandler(t *testing.T) {
	t.Parallel()

	t.Run("returns the rule", func(t *testing.T) {
		t.Parallel()
		srv := armaprfake.Server{
			GetByName: func(_ context.Context, rg, name string, _ *armalertprocessingrules.ClientGetByNameOptions) (azfake.Responder[armalertprocessingrules.ClientGetByNameResponse], azfake.ErrorResponder) {
				require.Equal(t, testResourceGroup, rg)
				require.Equal(t, "rule-a", name)
				var resp azfake.Responder[armalertprocessingrules.ClientGetByNameResponse]
				resp.SetResponse(http.StatusOK, armalertprocessingrules.ClientGetByNameResponse{AlertProcessingRule: *sampleRule("rule-a")}, nil)
				return resp, azfake.ErrorResponder{}
			},
		}
		handler := NewAlertProcessingRuleGetHandler(newFakeClient(t, srv), testResourceGroup)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/admin/v1/alertprocessingrules/rule-a", nil)
		req.SetPathValue("name", "rule-a")
		require.NoError(t, handler.ServeHTTP(rec, req))
		require.Equal(t, http.StatusOK, rec.Code)

		var resp AlertProcessingRuleSummary
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		require.Equal(t, "rule-a", resp.Name)
		require.Equal(t, "desc-rule-a", resp.Description)
	})

	t.Run("maps ARM 404 to NotFound CloudError", func(t *testing.T) {
		t.Parallel()
		srv := armaprfake.Server{
			GetByName: func(_ context.Context, _, _ string, _ *armalertprocessingrules.ClientGetByNameOptions) (azfake.Responder[armalertprocessingrules.ClientGetByNameResponse], azfake.ErrorResponder) {
				var errResp azfake.ErrorResponder
				errResp.SetResponseError(http.StatusNotFound, "ResourceNotFound")
				return azfake.Responder[armalertprocessingrules.ClientGetByNameResponse]{}, errResp
			},
		}
		handler := NewAlertProcessingRuleGetHandler(newFakeClient(t, srv), testResourceGroup)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/admin/v1/alertprocessingrules/missing", nil)
		req.SetPathValue("name", "missing")

		err := handler.ServeHTTP(rec, req)
		requireCloudError(t, err, http.StatusNotFound, "Not Found")
	})
}

func TestAlertProcessingRulePutHandler(t *testing.T) {
	t.Parallel()

	t.Run("builds a suppression rule from the request", func(t *testing.T) {
		t.Parallel()
		var captured armalertprocessingrules.AlertProcessingRule
		var capturedName, capturedRG string
		srv := armaprfake.Server{
			CreateOrUpdate: func(_ context.Context, rg, name string, rule armalertprocessingrules.AlertProcessingRule, _ *armalertprocessingrules.ClientCreateOrUpdateOptions) (azfake.Responder[armalertprocessingrules.ClientCreateOrUpdateResponse], azfake.ErrorResponder) {
				captured, capturedName, capturedRG = rule, name, rg
				var resp azfake.Responder[armalertprocessingrules.ClientCreateOrUpdateResponse]
				resp.SetResponse(http.StatusOK, armalertprocessingrules.ClientCreateOrUpdateResponse{AlertProcessingRule: rule}, nil)
				return resp, azfake.ErrorResponder{}
			},
		}
		handler := NewAlertProcessingRulePutHandler(newFakeClient(t, srv), testResourceGroup, testScopes)

		body := AlertProcessingRulePutRequest{
			AlertRuleName:     "MyAlert",
			ResourceFilter:    "np-skip-minor",
			StartTime:         "2026-09-22T00:00:00",
			EndTime:           "2026-09-23T00:00:00",
			IncidentReference: "ICM-123",
			Scopes:            testScopes,
		}
		rec := httptest.NewRecorder()
		req := newJSONRequest(t, http.MethodPut, "/admin/v1/alertprocessingrules/rule-a", body)
		req.SetPathValue("name", "rule-a")

		require.NoError(t, handler.ServeHTTP(rec, req))
		require.Equal(t, http.StatusOK, rec.Code)

		require.Equal(t, testResourceGroup, capturedRG)
		require.Equal(t, "rule-a", capturedName)
		require.Equal(t, "Global", derefString(captured.Location))
		require.NotNil(t, captured.Properties)
		require.True(t, *captured.Properties.Enabled, "enabled should default to true")
		require.Equal(t, testScopes, derefStringSlice(captured.Properties.Scopes))

		// Suppression action.
		require.Len(t, captured.Properties.Actions, 1)
		_, ok := captured.Properties.Actions[0].(*armalertprocessingrules.RemoveAllActionGroups)
		require.True(t, ok, "expected RemoveAllActionGroups action")

		// Conditions: alert rule name, firing-only, and the resource filter.
		nameCond := findCondition(captured.Properties.Conditions, armalertprocessingrules.FieldAlertRuleName)
		require.NotNil(t, nameCond)
		require.Equal(t, armalertprocessingrules.OperatorEquals, *nameCond.Operator)
		require.Equal(t, []string{"MyAlert"}, derefStringSlice(nameCond.Values))

		firingCond := findCondition(captured.Properties.Conditions, armalertprocessingrules.FieldMonitorCondition)
		require.NotNil(t, firingCond)
		require.Equal(t, []string{monitorConditionFired}, derefStringSlice(firingCond.Values))

		ctxCond := findCondition(captured.Properties.Conditions, armalertprocessingrules.FieldAlertContext)
		require.NotNil(t, ctxCond)
		require.Equal(t, armalertprocessingrules.OperatorContains, *ctxCond.Operator)
		require.Equal(t, []string{"np-skip-minor"}, derefStringSlice(ctxCond.Values))

		// Schedule and tags.
		require.Equal(t, "2026-09-22T00:00:00", derefString(captured.Properties.Schedule.EffectiveFrom))
		require.Equal(t, "2026-09-23T00:00:00", derefString(captured.Properties.Schedule.EffectiveUntil))
		require.Equal(t, aroHCPPurposeTagValue, derefString(captured.Tags[aroHCPPurposeTagKey]))
		require.Equal(t, "ICM-123", derefString(captured.Tags[incidentTagKey]))
	})

	t.Run("omits the AlertContext condition when no resource filter", func(t *testing.T) {
		t.Parallel()
		var captured armalertprocessingrules.AlertProcessingRule
		srv := armaprfake.Server{
			CreateOrUpdate: func(_ context.Context, _, _ string, rule armalertprocessingrules.AlertProcessingRule, _ *armalertprocessingrules.ClientCreateOrUpdateOptions) (azfake.Responder[armalertprocessingrules.ClientCreateOrUpdateResponse], azfake.ErrorResponder) {
				captured = rule
				var resp azfake.Responder[armalertprocessingrules.ClientCreateOrUpdateResponse]
				resp.SetResponse(http.StatusOK, armalertprocessingrules.ClientCreateOrUpdateResponse{AlertProcessingRule: rule}, nil)
				return resp, azfake.ErrorResponder{}
			},
		}
		handler := NewAlertProcessingRulePutHandler(newFakeClient(t, srv), testResourceGroup, testScopes)

		body := AlertProcessingRulePutRequest{
			AlertRuleName: "MyAlert",
			StartTime:     "2026-09-22T00:00:00",
			EndTime:       "2026-09-23T00:00:00",
			Description:   "planned maintenance",
			Enabled:       to.Ptr(false),
			Scopes:        testScopes,
		}
		rec := httptest.NewRecorder()
		req := newJSONRequest(t, http.MethodPut, "/admin/v1/alertprocessingrules/rule-a", body)
		req.SetPathValue("name", "rule-a")

		require.NoError(t, handler.ServeHTTP(rec, req))
		require.Nil(t, findCondition(captured.Properties.Conditions, armalertprocessingrules.FieldAlertContext))
		require.False(t, *captured.Properties.Enabled, "explicit enabled=false should be honored")
		require.Equal(t, "planned maintenance", derefString(captured.Properties.Description))
	})

	t.Run("accepts extra scopes when at least one known AMW is present", func(t *testing.T) {
		t.Parallel()
		var captured armalertprocessingrules.AlertProcessingRule
		srv := armaprfake.Server{
			CreateOrUpdate: func(_ context.Context, _, _ string, rule armalertprocessingrules.AlertProcessingRule, _ *armalertprocessingrules.ClientCreateOrUpdateOptions) (azfake.Responder[armalertprocessingrules.ClientCreateOrUpdateResponse], azfake.ErrorResponder) {
				captured = rule
				var resp azfake.Responder[armalertprocessingrules.ClientCreateOrUpdateResponse]
				resp.SetResponse(http.StatusOK, armalertprocessingrules.ClientCreateOrUpdateResponse{AlertProcessingRule: rule}, nil)
				return resp, azfake.ErrorResponder{}
			},
		}
		handler := NewAlertProcessingRulePutHandler(newFakeClient(t, srv), testResourceGroup, testScopes)

		extraScope := "/subscriptions/" + testSubscription + "/resourceGroups/other/providers/microsoft.monitor/accounts/amw-other"
		scopes := append(slices.Clone(testScopes), extraScope)
		body := AlertProcessingRulePutRequest{
			AlertRuleName: "MyAlert",
			StartTime:     "2026-09-22T00:00:00",
			EndTime:       "2026-09-23T00:00:00",
			Description:   "planned maintenance",
			Scopes:        scopes,
		}
		rec := httptest.NewRecorder()
		req := newJSONRequest(t, http.MethodPut, "/admin/v1/alertprocessingrules/rule-a", body)
		req.SetPathValue("name", "rule-a")

		require.NoError(t, handler.ServeHTTP(rec, req))
		require.Equal(t, http.StatusOK, rec.Code)
		// All scopes (known AMW plus extras) are forwarded to ARM unchanged.
		require.Equal(t, scopes, derefStringSlice(captured.Properties.Scopes))
	})

	t.Run("accepts an AMW scope that differs only in casing", func(t *testing.T) {
		t.Parallel()
		var captured armalertprocessingrules.AlertProcessingRule
		srv := armaprfake.Server{
			CreateOrUpdate: func(_ context.Context, _, _ string, rule armalertprocessingrules.AlertProcessingRule, _ *armalertprocessingrules.ClientCreateOrUpdateOptions) (azfake.Responder[armalertprocessingrules.ClientCreateOrUpdateResponse], azfake.ErrorResponder) {
				captured = rule
				var resp azfake.Responder[armalertprocessingrules.ClientCreateOrUpdateResponse]
				resp.SetResponse(http.StatusOK, armalertprocessingrules.ClientCreateOrUpdateResponse{AlertProcessingRule: rule}, nil)
				return resp, azfake.ErrorResponder{}
			},
		}
		configuredScope := "/subscriptions/" + testSubscription + "/resourceGroups/" + testResourceGroup + "/providers/Microsoft.Monitor/accounts/hcps-region"
		requestScope := "/subscriptions/" + testSubscription + "/resourcegroups/" + testResourceGroup + "/providers/microsoft.monitor/accounts/HCPS-REGION"
		handler := NewAlertProcessingRulePutHandler(newFakeClient(t, srv), testResourceGroup, []string{configuredScope})

		body := AlertProcessingRulePutRequest{
			AlertRuleName: "MyAlert",
			StartTime:     "2026-09-22T00:00:00",
			EndTime:       "2026-09-23T00:00:00",
			Description:   "planned maintenance",
			Scopes:        []string{requestScope},
		}
		rec := httptest.NewRecorder()
		req := newJSONRequest(t, http.MethodPut, "/admin/v1/alertprocessingrules/rule-a", body)
		req.SetPathValue("name", "rule-a")

		require.NoError(t, handler.ServeHTTP(rec, req))
		require.Equal(t, http.StatusOK, rec.Code)
		// The client's parsed scope is forwarded to ARM with its provider-namespace
		// and resource-name casing preserved (e.g. "HCPS-REGION", not lowercased).
		wantParsed, err := azcorearm.ParseResourceID(requestScope)
		require.NoError(t, err, "request scope should parse")
		require.Equal(t, []string{wantParsed.String()}, derefStringSlice(captured.Properties.Scopes))
	})

	t.Run("validation failures return 400 and do not call ARM", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			body AlertProcessingRulePutRequest
			msg  string
		}{
			{
				name: "missing alertRuleName",
				body: AlertProcessingRulePutRequest{StartTime: "s", EndTime: "e", Description: "r"},
				msg:  "alertRuleName",
			},
			{
				name: "missing times",
				body: AlertProcessingRulePutRequest{AlertRuleName: "a", Description: "r"},
				msg:  "startTime",
			},
			{
				name: "missing reason and incident",
				body: AlertProcessingRulePutRequest{AlertRuleName: "a", StartTime: "s", EndTime: "e"},
				msg:  "description",
			},
			{
				name: "empty scopes",
				body: AlertProcessingRulePutRequest{AlertRuleName: "a", StartTime: "s", EndTime: "e", Description: "r"},
				msg:  "scopes",
			},
			{
				name: "scopes without a known AMW",
				body: AlertProcessingRulePutRequest{AlertRuleName: "a", StartTime: "s", EndTime: "e", Description: "r", Scopes: []string{"/subscriptions/" + testSubscription + "/resourceGroups/other/providers/microsoft.monitor/accounts/amw-unknown"}},
				msg:  "scopes",
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				srv := armaprfake.Server{
					CreateOrUpdate: func(_ context.Context, _, _ string, _ armalertprocessingrules.AlertProcessingRule, _ *armalertprocessingrules.ClientCreateOrUpdateOptions) (azfake.Responder[armalertprocessingrules.ClientCreateOrUpdateResponse], azfake.ErrorResponder) {
						t.Errorf("CreateOrUpdate must not be called for invalid input")
						return azfake.Responder[armalertprocessingrules.ClientCreateOrUpdateResponse]{}, azfake.ErrorResponder{}
					},
				}
				handler := NewAlertProcessingRulePutHandler(newFakeClient(t, srv), testResourceGroup, testScopes)

				rec := httptest.NewRecorder()
				req := newJSONRequest(t, http.MethodPut, "/admin/v1/alertprocessingrules/rule-a", tc.body)
				req.SetPathValue("name", "rule-a")

				err := handler.ServeHTTP(rec, req)
				requireCloudError(t, err, http.StatusBadRequest, tc.msg)
			})
		}
	})
}

func TestAlertProcessingRuleDeleteHandler(t *testing.T) {
	t.Parallel()

	t.Run("returns 204 on success", func(t *testing.T) {
		t.Parallel()
		srv := armaprfake.Server{
			Delete: func(_ context.Context, rg, name string, _ *armalertprocessingrules.ClientDeleteOptions) (azfake.Responder[armalertprocessingrules.ClientDeleteResponse], azfake.ErrorResponder) {
				require.Equal(t, testResourceGroup, rg)
				require.Equal(t, "rule-a", name)
				var resp azfake.Responder[armalertprocessingrules.ClientDeleteResponse]
				resp.SetResponse(http.StatusOK, armalertprocessingrules.ClientDeleteResponse{}, nil)
				return resp, azfake.ErrorResponder{}
			},
		}
		handler := NewAlertProcessingRuleDeleteHandler(newFakeClient(t, srv), testResourceGroup)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/admin/v1/alertprocessingrules/rule-a", nil)
		req.SetPathValue("name", "rule-a")

		require.NoError(t, handler.ServeHTTP(rec, req))
		require.Equal(t, http.StatusNoContent, rec.Code)
		require.Empty(t, rec.Body.String())
	})

	t.Run("maps ARM 404 to NotFound CloudError", func(t *testing.T) {
		t.Parallel()
		srv := armaprfake.Server{
			Delete: func(_ context.Context, _, _ string, _ *armalertprocessingrules.ClientDeleteOptions) (azfake.Responder[armalertprocessingrules.ClientDeleteResponse], azfake.ErrorResponder) {
				var errResp azfake.ErrorResponder
				errResp.SetResponseError(http.StatusNotFound, "ResourceNotFound")
				return azfake.Responder[armalertprocessingrules.ClientDeleteResponse]{}, errResp
			},
		}
		handler := NewAlertProcessingRuleDeleteHandler(newFakeClient(t, srv), testResourceGroup)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/admin/v1/alertprocessingrules/missing", nil)
		req.SetPathValue("name", "missing")

		err := handler.ServeHTTP(rec, req)
		requireCloudError(t, err, http.StatusNotFound, "Not Found")
	})
}

func newJSONRequest(t *testing.T, method, target string, body any) *http.Request {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	return httptest.NewRequest(method, target, bytes.NewReader(data))
}

func requireCloudError(t *testing.T, err error, wantStatus int, wantMsg string) {
	t.Helper()
	require.Error(t, err)
	var cloudErr *coreapi.CloudError
	require.True(t, errors.As(err, &cloudErr), "expected CloudError, got %T: %v", err, err)
	require.Equal(t, wantStatus, cloudErr.StatusCode)
	require.Contains(t, cloudErr.Error(), wantMsg)
}

func derefStringSlice(in []*string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		out = append(out, derefString(v))
	}
	return out
}
