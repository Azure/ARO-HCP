package frontend

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

type CloudErrorContainer struct {
	Error arm.CloudErrorBody `json:"error"`
}

func TestMiddlewareValidateStatic(t *testing.T) {
	// This will act as the next handler if middleware validation passes
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // indicate success
	})

	tests := []struct {
		name string
		path string

		operationsId       string
		expectedStatusCode int
		expectedBody       string
	}{
		{
			name:               "Valid request",
			path:               "/Subscriptions/42d9eac4-d29a-4d6e-9e26-3439758b1491/ResourceGroups/MyResourceGroup/Providers/Microsoft.RedHatOpenShift/HCPOpenShiftClusters/MyCluster",
			expectedStatusCode: http.StatusOK,
		},
		{
			name:               "Invalid subscription ID",
			path:               "/Subscriptions/invalid!sub!id",
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       "The provided subscription identifier 'invalid!sub!id' is malformed or invalid.",
		},
		{
			name:               "Invalid resource group name",
			path:               "/Subscriptions/00000000-0000-0000-0000-000000000000/ResourceGroups/ResourceGroup!",
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       "Resource group 'ResourceGroup!' is invalid.",
		},
		{
			name:               "Invalid resource name",
			path:               "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000/RESOURCEGROUPS/MyResourceGroup/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/$",
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       "The Resource 'MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/$' under resource group 'MyResourceGroup' is invalid.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://example.com"+tc.path, nil)
			req = req.WithContext(ContextWithOriginalPath(req.Context(), tc.path))

			// Use httptest.ResponseRecorder to record the response
			w := httptest.NewRecorder()

			// Execute the middleware
			MiddlewareValidateStatic(w, req, nextHandler)

			// Check the response status code
			if status := w.Code; status != tc.expectedStatusCode {
				t.Errorf("handler returned wrong status code: got %v want %v",
					status, tc.expectedStatusCode)
			}

			if tc.expectedStatusCode != http.StatusOK {

				var resp CloudErrorContainer
				body, err := io.ReadAll(http.MaxBytesReader(w, w.Result().Body, 4*megabyte))
				if err != nil {
					t.Fatalf("failed to read response body: %v", err)
				}
				err = json.Unmarshal(body, &resp)
				if err != nil {
					t.Fatalf("failed to unmarshal response body: %v", err)
				}

				// Check if the error message contains the expected text
				if !strings.Contains(resp.Error.Message, tc.expectedBody) {
					t.Errorf("handler returned unexpected body: got %v want %v",
						resp.Error.Message, tc.expectedBody)
				}
			}
		})
	}
}
