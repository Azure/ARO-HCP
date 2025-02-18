package arm

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestNewCorrelationData(t *testing.T) {
	const (
		client_request_id             = "random_client_request_id"
		correlation_request_id string = "random_correlation_request_id"
	)

	tests := []struct {
		name    string
		request *http.Request
		want    *CorrelationData
	}{
		{
			name: "NewCorrelationData returns the appropriate correlation data from request",
			request: &http.Request{
				Header: http.Header{
					HeaderNameClientRequestID:      []string{client_request_id},
					HeaderNameCorrelationRequestID: []string{correlation_request_id},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			correlationData := NewCorrelationData(tt.request)

			if correlationData.RequestID == uuid.Nil {
				t.Fatalf("correlationData.RequestID is nil")
			}

			if correlationData.ClientRequestID != client_request_id {
				t.Errorf("got %v, but want %v", correlationData.ClientRequestID, client_request_id)
			}

			if correlationData.CorrelationRequestID != correlation_request_id {
				t.Errorf("got %v, but want %v", correlationData.CorrelationRequestID, correlation_request_id)
			}
		})
	}
}
