package frontend

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
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

			f := &Frontend{
				dbClient: mockDBClient,
				metrics:  NewPrometheusEmitter(reg),
			}
			f.ready.Store(test.ready)
			ts := httptest.NewServer(f.routes())
			ts.Config.BaseContext = func(net.Listener) context.Context {
				return ContextWithLogger(context.Background(), testLogger)
			}

			// Call expected but is irrelevant to the test.
			mockDBClient.EXPECT().DBConnectionTest(gomock.Any())

			rs, err := ts.Client().Get(ts.URL + "/healthz")
			if err != nil {
				t.Fatal(err)
			}

			if rs.StatusCode != test.expectedStatusCode {
				t.Errorf("expected status code %d, got %d", test.expectedStatusCode, rs.StatusCode)
			}

			lintMetrics(t, reg)
		})
	}
}

func TestSubscriptionsGET(t *testing.T) {
	tests := []struct {
		name               string
		subDoc             *database.SubscriptionDocument
		expectedStatusCode int
	}{
		{
			name: "GET Subscription - Doc Exists",
			subDoc: database.NewSubscriptionDocument(subscriptionID,
				&arm.Subscription{
					State:            arm.SubscriptionStateRegistered,
					RegistrationDate: api.Ptr(time.Now().String()),
					Properties:       nil,
				}),
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

			f := &Frontend{
				dbClient: mockDBClient,
				metrics:  NewPrometheusEmitter(reg),
			}

			// ArmSubscriptionGet and MetricsMiddleware
			mockDBClient.EXPECT().
				GetSubscriptionDoc(gomock.Any(), gomock.Any()).
				Return(getMockDBDoc(test.subDoc)).
				Times(2)

			ts := httptest.NewServer(f.routes())
			ts.Config.BaseContext = func(net.Listener) context.Context {
				ctx := context.Background()
				ctx = ContextWithLogger(ctx, testLogger)
				ctx = ContextWithDBClient(ctx, f.dbClient)
				return ctx
			}

			rs, err := ts.Client().Get(ts.URL + "/subscriptions/" + subscriptionID + "?api-version=2.0")
			if err != nil {
				t.Fatal(err)
			}

			if rs.StatusCode != test.expectedStatusCode {
				t.Errorf("expected status code %d, got %d", test.expectedStatusCode, rs.StatusCode)
			}

			lintMetrics(t, reg)
		})
	}
}

func TestSubscriptionsPUT(t *testing.T) {
	tests := []struct {
		name               string
		urlPath            string
		subscription       *arm.Subscription
		subDoc             *database.SubscriptionDocument
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
			subDoc: database.NewSubscriptionDocument(subscriptionID,
				&arm.Subscription{
					State:            arm.SubscriptionStateRegistered,
					RegistrationDate: api.Ptr(time.Now().String()),
					Properties:       nil,
				}),
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

			f := &Frontend{
				dbClient: mockDBClient,
				metrics:  NewPrometheusEmitter(reg),
			}

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
						CreateSubscriptionDoc(gomock.Any(), gomock.Any())
				} else {
					mockDBClient.EXPECT().
						UpdateSubscriptionDoc(gomock.Any(), gomock.Any(), gomock.Any())
				}
			}
			// MiddlewareMetrics
			// (except when MiddlewareValidateStatic fails)
			mockDBClient.EXPECT().
				GetSubscriptionDoc(gomock.Any(), gomock.Any()).
				Return(database.NewSubscriptionDocument(subscriptionID, test.subscription), nil).
				MaxTimes(1)

			ts := httptest.NewServer(f.routes())
			ts.Config.BaseContext = func(net.Listener) context.Context {
				ctx := context.Background()
				ctx = ContextWithLogger(ctx, testLogger)
				ctx = ContextWithDBClient(ctx, f.dbClient)
				return ctx
			}

			req, err := http.NewRequest(http.MethodPut, ts.URL+test.urlPath, bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")

			rs, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}

			if rs.StatusCode != test.expectedStatusCode {
				t.Errorf("expected status code %d, got %d", test.expectedStatusCode, rs.StatusCode)
			}

			lintMetrics(t, reg)
		})
	}
}

func lintMetrics(t *testing.T, r prometheus.Gatherer) {
	t.Helper()

	problems, err := testutil.GatherAndLint(r)
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range problems {
		t.Errorf("metric %q: %s", p.Metric, p.Text)
	}
}
