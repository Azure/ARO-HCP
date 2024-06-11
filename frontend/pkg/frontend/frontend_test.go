package frontend

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/ARO-HCP/frontend/pkg/database"
)

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
			f := &Frontend{
				dbClient: database.NewCache(),
				logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
				metrics:  NewPrometheusEmitter(),
			}
			f.ready.Store(test.ready)
			ts := httptest.NewServer(f.routes())
			ts.Config.BaseContext = func(net.Listener) context.Context {
				return ContextWithLogger(context.Background(), f.logger)
			}

			rs, err := ts.Client().Get(ts.URL + "/healthz/ready")
			if err != nil {
				t.Fatal(err)
			}

			if rs.StatusCode != test.expectedStatusCode {
				t.Errorf("expected status code %d, got %d", test.expectedStatusCode, rs.StatusCode)
			}
		})
	}
}
