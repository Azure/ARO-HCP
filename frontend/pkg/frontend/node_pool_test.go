package frontend

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
)

const dummySubscrtiptionId = "00000000-0000-0000-0000-000000000000"
const dummyResourceGroupId = "dummy_resource_group_name"
const dummyCluster = "dev-test-cluster"
const dummyNodePool = "dev-nodepool"

const URLPrefix = ("/subscriptions/" + dummySubscrtiptionId + "/resourcegroups/" + dummyResourceGroupId +
	"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + dummyCluster + "/nodePools/" + dummyNodePool)

func TestCreateOrUpdateNodePool(t *testing.T) {
	tests := []struct {
		name               string
		urlPath            string
		subscription       *arm.Subscription
		subDoc             *database.SubscriptionDocument
		expectedStatusCode int
	}{
		{
			name:    "PUT Node Pool - Create a new Node Pool",
			urlPath: URLPrefix + "?api-version=2024-06-10-preview",
			subscription: &arm.Subscription{
				State:            arm.SubscriptionStateRegistered,
				RegistrationDate: api.Ptr(time.Now().String()),
				Properties:       nil,
			},
			subDoc:             nil,
			expectedStatusCode: http.StatusCreated,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := &Frontend{
				dbClient: database.NewCache(),
				logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
				metrics:  NewPrometheusEmitter(),
			}

			if test.subDoc != nil {
				err := f.dbClient.SetSubscriptionDoc(context.TODO(), test.subDoc)
				if err != nil {
					t.Fatal(err)
				}
			}

			ts := httptest.NewServer(f.routes())
			ts.Config.BaseContext = func(net.Listener) context.Context {
				return ContextWithLogger(context.Background(), f.logger)
			}

			req, err := http.NewRequest(http.MethodPut, ts.URL+test.urlPath, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")

			rs, err := ts.Client().Do(req)
			t.Log(rs)
			if err != nil {
				t.Log(err)
				t.Log("AAAAAAAAAAAAAAAAaaaa")
				t.Fatal(err)
			}

			if rs.StatusCode != test.expectedStatusCode {
				t.Errorf("expected status code %d, got %d", test.expectedStatusCode, rs.StatusCode)
			}
		})
	}
}
