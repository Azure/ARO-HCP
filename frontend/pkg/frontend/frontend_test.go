package frontend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/mocks"
)

var testLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

const subscriptionID = "00000000-0000-0000-0000-000000000000"

func getMockDBDoc[T any](t *T) (*T, error) {
	if t != nil {
		return t, nil
	} else {
		return nil, database.ErrNotFound
	}
}

func equalResourceID(expectResourceID *azcorearm.ResourceID) gomock.Matcher {
	return gomock.Cond(func(actualResourceID *azcorearm.ResourceID) bool {
		return strings.EqualFold(actualResourceID.String(), expectResourceID.String())
	})
}

func TestReadiness(t *testing.T) {
	tests := []struct {
		name               string
		ready              bool
		expectedStatusCode int
	}{
		{
			name:               "Not ready - returns 500",
			ready:              false,
			expectedStatusCode: http.StatusInternalServerError,
		},
		{
			name:               "Ready - returns 200",
			ready:              true,
			expectedStatusCode: http.StatusOK,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockDBClient := mocks.NewMockDBClient(ctrl)
			reg := prometheus.NewRegistry()

			f := NewFrontend(
				testLogger,
				nil,
				nil,
				reg,
				mockDBClient,
				"",
				nil,
			)
			f.ready.Store(test.ready)

			mockDBClient.EXPECT().DBConnectionTest(gomock.Any())

			ts := newHTTPServer(f, ctrl, mockDBClient, nil)

			rs, err := ts.Client().Get(ts.URL + "/healthz")
			require.NoError(t, err)
			require.Equal(t, test.expectedStatusCode, rs.StatusCode)

			lintMetrics(t, reg)

			got, err := testutil.GatherAndCount(reg, healthGaugeName)
			require.NoError(t, err)
			assert.Equal(t, 1, got)
		})
	}
}

func TestSubscriptionsGET(t *testing.T) {
	tests := []struct {
		name               string
		subDoc             *arm.Subscription
		expectedStatusCode int
	}{
		{
			name: "GET Subscription - Doc Exists",
			subDoc: &arm.Subscription{
				State:            arm.SubscriptionStateRegistered,
				RegistrationDate: api.Ptr(time.Now().String()),
				Properties:       nil,
			},
			expectedStatusCode: http.StatusOK,
		},
		{
			name:               "GET Subscription - No Doc",
			subDoc:             nil,
			expectedStatusCode: http.StatusNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockDBClient := mocks.NewMockDBClient(ctrl)
			reg := prometheus.NewRegistry()

			f := NewFrontend(
				testLogger,
				nil,
				nil,
				reg,
				mockDBClient,
				"",
				nil,
			)

			// ArmSubscriptionGet.
			mockDBClient.EXPECT().
				GetSubscriptionDoc(gomock.Any(), gomock.Any()).
				Return(getMockDBDoc(test.subDoc)).
				Times(1)

			// The subscription collector lists all documents once.
			subs := make(map[string]*arm.Subscription)
			if test.subDoc != nil {
				subs[subscriptionID] = test.subDoc
			}
			ts := newHTTPServer(f, ctrl, mockDBClient, subs)

			rs, err := ts.Client().Get(ts.URL + "/subscriptions/" + subscriptionID + "?api-version=2.0")
			if err != nil {
				t.Fatal(err)
			}

			if rs.StatusCode != test.expectedStatusCode {
				t.Errorf("expected status code %d, got %d", test.expectedStatusCode, rs.StatusCode)
			}

			lintMetrics(t, reg)
			assertHTTPMetrics(t, reg, test.subDoc)
		})
	}
}

func TestSubscriptionsPUT(t *testing.T) {
	tests := []struct {
		name               string
		urlPath            string
		subscription       *arm.Subscription
		subDoc             *arm.Subscription
		expectedStatusCode int
	}{
		{
			name:    "PUT Subscription - Doc does not exist",
			urlPath: "/subscriptions/" + subscriptionID + "?api-version=2.0",
			subscription: &arm.Subscription{
				State:            arm.SubscriptionStateRegistered,
				RegistrationDate: api.Ptr(time.Now().String()),
				Properties:       nil,
			},
			subDoc:             nil,
			expectedStatusCode: http.StatusOK,
		},
		{
			name:    "PUT Subscription - Doc Exists",
			urlPath: "/subscriptions/" + subscriptionID + "?api-version=2.0",
			subscription: &arm.Subscription{
				State:            arm.SubscriptionStateRegistered,
				RegistrationDate: api.Ptr(time.Now().String()),
				Properties:       nil,
			},
			subDoc: &arm.Subscription{
				State:            arm.SubscriptionStateRegistered,
				RegistrationDate: api.Ptr(time.Now().String()),
				Properties:       nil,
			},
			expectedStatusCode: http.StatusOK,
		},
		{
			name:    "PUT Subscription - Invalid Subscription",
			urlPath: "/subscriptions/oopsie-i-no-good0?api-version=2.0",
			subscription: &arm.Subscription{
				State:            arm.SubscriptionStateRegistered,
				RegistrationDate: api.Ptr(time.Now().String()),
				Properties:       nil,
			},
			subDoc:             nil,
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			name:    "PUT Subscription - Missing State",
			urlPath: "/subscriptions/" + subscriptionID + "?api-version=2.0",
			subscription: &arm.Subscription{
				RegistrationDate: api.Ptr(time.Now().String()),
				Properties:       nil,
			},
			subDoc:             nil,
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			name:    "PUT Subscription - Invalid State",
			urlPath: "/subscriptions/" + subscriptionID + "?api-version=2.0",
			subscription: &arm.Subscription{
				State:            "Bogus",
				RegistrationDate: api.Ptr(time.Now().String()),
				Properties:       nil,
			},
			subDoc:             nil,
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			name:    "PUT Subscription - Missing RegistrationDate",
			urlPath: "/subscriptions/" + subscriptionID + "?api-version=2.0",
			subscription: &arm.Subscription{
				State:      arm.SubscriptionStateRegistered,
				Properties: nil,
			},
			subDoc:             nil,
			expectedStatusCode: http.StatusBadRequest,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockDBClient := mocks.NewMockDBClient(ctrl)
			reg := prometheus.NewRegistry()

			f := NewFrontend(
				testLogger,
				nil,
				nil,
				reg,
				mockDBClient,
				"",
				nil,
			)

			body, err := json.Marshal(&test.subscription)
			if err != nil {
				t.Fatal(err)
			}

			// MiddlewareLockSubscription
			// (except when MiddlewareValidateStatic fails)
			mockDBClient.EXPECT().
				GetLockClient().
				MaxTimes(1)
			if test.expectedStatusCode != http.StatusBadRequest {
				// ArmSubscriptionPut
				mockDBClient.EXPECT().
					GetSubscriptionDoc(gomock.Any(), gomock.Any()).
					Return(getMockDBDoc(test.subDoc))
				// ArmSubscriptionPut
				if test.subDoc == nil {
					mockDBClient.EXPECT().
						CreateSubscriptionDoc(gomock.Any(), gomock.Any(), gomock.Any())
				} else {
					mockDBClient.EXPECT().
						UpdateSubscriptionDoc(gomock.Any(), gomock.Any(), gomock.Any())
				}
			}

			subs := make(map[string]*arm.Subscription)
			if test.subDoc != nil {
				subs[subscriptionID] = test.subDoc
			}
			ts := newHTTPServer(f, ctrl, mockDBClient, subs)

			req, err := http.NewRequest(http.MethodPut, ts.URL+test.urlPath, bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")

			rs, err := ts.Client().Do(req)
			require.NoError(t, err)

			assert.Equal(t, test.expectedStatusCode, rs.StatusCode)

			lintMetrics(t, reg)
			if test.expectedStatusCode != http.StatusBadRequest {
				assertHTTPMetrics(t, reg, test.subDoc)
			}
		})
	}
}

func TestDeploymentPreflight(t *testing.T) {
	tests := []struct {
		name         string
		resource     map[string]any
		expectStatus arm.DeploymentPreflightStatus
		expectErrors int
	}{
		{
			name: "Unhandled resource type returns no error",
			resource: map[string]any{
				"name":       "virtual-machine",
				"type":       "Microsoft.Compute/virtualMachines",
				"location":   "eastus",
				"apiVersion": "2024-07-01",
			},
			expectStatus: arm.DeploymentPreflightStatusSucceeded,
		},
		{
			name: "Unrecognized API version returns no error",
			resource: map[string]any{
				"name":       "my-hcp-cluster",
				"type":       api.ClusterResourceType.String(),
				"location":   "eastus",
				"apiVersion": "1980-01-01",
			},
			expectStatus: arm.DeploymentPreflightStatusSucceeded,
		},
		{
			name: "Well-formed cluster resource returns no error",
			resource: map[string]any{
				"name":       "my-hcp-cluster",
				"type":       api.ClusterResourceType.String(),
				"location":   "eastus",
				"apiVersion": "2024-06-10-preview",
				"properties": map[string]any{
					"version": map[string]any{
						"id":           "4.0.0",
						"channelGroup": "stable",
					},
					"api": map[string]any{
						"visibility": "public",
					},
					"platform": map[string]any{
						"subnetId":               "/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.Network/virtualNetworks/MyVNet/subnets",
						"networkSecurityGroupId": "/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.Network/networkSecurityGroups/MyNSG",
					},
				},
			},
			expectStatus: arm.DeploymentPreflightStatusSucceeded,
		},
		{
			name: "Preflight catches cluster resource with invalid fields",
			resource: map[string]any{
				"name":       "my-hcp-cluster",
				"type":       api.ClusterResourceType.String(),
				"location":   "eastus",
				"apiVersion": "2024-06-10-preview",
				"properties": map[string]any{
					"version": map[string]any{
						"channelGroup": "stable",
					},
					"network": map[string]any{
						// 1 invalid fields
						"podCidr": "invalidCidr",
					},
					"api": map[string]any{
						// 1 invalid field
						"visibility": "invisible",
					},
					"platform": map[string]any{
						// 2 missing required fields
					},
				},
			},
			expectStatus: arm.DeploymentPreflightStatusFailed,
			expectErrors: 4,
		},
		{
			name: "Well-formed node pool resource returns no error",
			resource: map[string]any{
				"name":       "my-node-pool",
				"type":       api.NodePoolResourceType.String(),
				"location":   "eastus",
				"apiVersion": "2024-06-10-preview",
				"properties": map[string]any{
					"version": map[string]any{
						"channelGroup": "stable",
					},
					"platform": map[string]any{
						"vmSize": "Standard_D8s_v3",
					},
				},
			},
			expectStatus: arm.DeploymentPreflightStatusSucceeded,
		},
		{
			name: "Preflight catches node pool resource with invalid fields",
			resource: map[string]any{
				"name":       "my-node-pool",
				"type":       api.NodePoolResourceType.String(),
				"location":   "eastus",
				"apiVersion": "2024-06-10-preview",
				"properties": map[string]any{
					"version": map[string]any{
						"channelGroup": "stable",
					},
					"platform": map[string]any{
						// 1 missing required field
					},
					"autoScaling": map[string]any{
						// 1 invalid field
						"min": 3,
						"max": 1,
					},
					"taints": []map[string]any{
						{
							// 1 invalid + 1 missing required field
							"effect": "NoTouchy",
						},
					},
				},
			},
			expectStatus: arm.DeploymentPreflightStatusFailed,
			expectErrors: 4,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			preflightPath := fmt.Sprintf("/subscriptions/%s/resourceGroups/myRG/providers/%s/deployments/myDeployment/preflight", subscriptionID, api.ProviderNamespace)

			ctrl := gomock.NewController(t)
			mockDBClient := mocks.NewMockDBClient(ctrl)
			reg := prometheus.NewRegistry()

			f := NewFrontend(
				testLogger,
				nil,
				nil,
				reg,
				mockDBClient,
				"",
				nil,
			)

			// MiddlewareValidateSubscriptionState and MetricsMiddleware
			mockDBClient.EXPECT().
				GetSubscriptionDoc(gomock.Any(), subscriptionID).
				Return(&arm.Subscription{
					State: arm.SubscriptionStateRegistered,
				}, nil).
				MaxTimes(2)

			subs := map[string]*arm.Subscription{
				subscriptionID: &arm.Subscription{
					State: arm.SubscriptionStateRegistered,
				},
			}
			ts := newHTTPServer(f, ctrl, mockDBClient, subs)

			resource, err := json.Marshal(&test.resource)
			require.NoError(t, err)
			preflightReq := arm.DeploymentPreflight{
				Resources: []json.RawMessage{resource},
			}
			body, err := json.Marshal(&preflightReq)
			require.NoError(t, err)

			req, err := http.NewRequest(http.MethodPost, ts.URL+preflightPath, bytes.NewReader(body))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")

			resp, err := ts.Client().Do(req)
			require.NoError(t, err)

			assert.Equal(t, http.StatusOK, resp.StatusCode)

			defer resp.Body.Close()
			body, err = io.ReadAll(resp.Body)
			require.NoError(t, err)

			var preflightResp arm.DeploymentPreflightResponse
			err = json.Unmarshal(body, &preflightResp)
			require.NoError(t, err)

			assert.Equal(t, test.expectStatus, preflightResp.Status)
			switch test.expectErrors {
			case 0:
				assert.Nil(t, preflightResp.Error)
			case 1:
				if assert.NotNil(t, preflightResp.Error) {
					assert.Nil(t, preflightResp.Error.Details)
					assert.NotEmpty(t, preflightResp.Error.Code)
					assert.NotEmpty(t, preflightResp.Error.Message)
					assert.NotEmpty(t, preflightResp.Error.Target)
				}
			default:
				if assert.NotNil(t, preflightResp.Error) {
					assert.Equal(t, test.expectErrors, len(preflightResp.Error.Details))
				}
			}
		})
	}
}

func lintMetrics(t *testing.T, r prometheus.Gatherer) {
	t.Helper()

	problems, err := testutil.GatherAndLint(r)
	require.NoError(t, err)

	for _, p := range problems {
		t.Errorf("metric %q: %s", p.Metric, p.Text)
	}
}

// assertHTTPMetrics ensures that HTTP metrics have been recorded.
func assertHTTPMetrics(t *testing.T, r prometheus.Gatherer, subscription *arm.Subscription) {
	t.Helper()

	metrics, err := r.Gather()
	assert.NoError(t, err)

	var mfs []*dto.MetricFamily
	for _, mf := range metrics {
		if mf.GetName() != requestCounterName && mf.GetName() != requestDurationName {
			continue
		}

		mfs = append(mfs, mf)

		for _, m := range mf.GetMetric() {
			var (
				route      string
				apiVersion string
				state      string
			)
			for _, l := range m.GetLabel() {
				switch l.GetName() {
				case "route":
					route = l.GetValue()
				case "api_version":
					apiVersion = l.GetValue()
				case "state":
					state = l.GetValue()
				}
			}

			// Verify that route and API version labels have known values.
			assert.NotEmpty(t, route)
			assert.NotEqual(t, route, noMatchRouteLabel)
			assert.NotEmpty(t, apiVersion)
			assert.NotEqual(t, apiVersion, unknownVersionLabel)

			if mf.GetName() == requestCounterName {
				assert.NotEmpty(t, state)
				if subscription != nil {
					assert.Equal(t, string(subscription.State), state)
				} else {
					assert.Equal(t, "Unknown", state)
				}
			}
		}
	}

	// We need request counter and latency histogram.
	assert.Len(t, mfs, 2)
}

// newHTTPServer returns a test HTTP server. When a mock DB client is provided,
// the subscription collector will be bootstrapped with the provided
// subscription documents.
func newHTTPServer(f *Frontend, ctrl *gomock.Controller, mockDBClient *mocks.MockDBClient, subs map[string]*arm.Subscription) *httptest.Server {
	ts := httptest.NewUnstartedServer(f.server.Handler)
	ts.Config.BaseContext = f.server.BaseContext
	ts.Start()

	mockIter := mocks.NewMockDBClientIterator[arm.Subscription](ctrl)
	mockIter.EXPECT().
		Items(gomock.Any()).
		Return(database.DBClientIteratorItem[arm.Subscription](maps.All(subs)))

	mockIter.EXPECT().
		GetError().
		Return(nil)

	mockDBClient.EXPECT().
		ListAllSubscriptionDocs().
		Return(mockIter).
		Times(1)

	// The initialization of the subscriptions collector is normally part of
	// the Run() method but the method doesn't get called in the tests so it's
	// executed here.
	stop := make(chan struct{})
	close(stop)
	f.collector.Run(testLogger, stop)

	return ts
}
