package arm


import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
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

			assert.NotEqual(t, uuid.Nil, correlationData.RequestID)
			assert.Equal(t, client_request_id, correlationData.ClientRequestID)
			assert.Equal(t, correlation_request_id, correlationData.CorrelationRequestID)
		})
	}
}
