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
		name              string
		path              string
		subscriptionID    string
		resourceGroupName string

		resourceType       string
		resourceName       string
		operationsId       string
		expectedStatusCode int
		expectedBody       string
	}{
		{
			name:               "Valid request",
			path:               "/subscriptions/42d9eac4-d29a-4d6e-9e26-3439758b1491",
			subscriptionID:     "42d9eac4-d29a-4d6e-9e26-3439758b1491",
			expectedStatusCode: http.StatusOK,
		},
		{
			name:               "Invalid subscription ID",
			path:               "/subscriptions/invalid!sub!id",
			subscriptionID:     "invalid!sub!id",
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       "The provided subscription identifier 'invalid!sub!id' is malformed or invalid.",
		},
		{
			name:               "Invalid resource group name",
			path:               "/resourcegroups/resourcegroup!",
			resourceGroupName:  "resourcegroup!",
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       "Resource group 'resourcegroup!' is invalid.",
		},
		{
			name:               "Invalid resource name",
			path:               "/resourcegroup/providers/microsoft.redhatopenshift/hcpopenshiftcluster/$",
			resourceGroupName:  "resourcegroup",
			resourceType:       "hcpOpenShiftClusters",
			resourceName:       "$",
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       "The Resource 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters/$' under resource group 'resourcegroup' is invalid.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://example.com"+tc.path, nil)

			// Use httptest.ResponseRecorder to record the response
			w := httptest.NewRecorder()

			req.SetPathValue(PathSegmentSubscriptionID, tc.subscriptionID)
			req.SetPathValue(PathSegmentResourceGroupName, tc.resourceGroupName)
			req.SetPathValue(PathSegmentResourceName, tc.resourceName)

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
