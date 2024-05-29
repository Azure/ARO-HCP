package frontend

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Azure/ARO-HCP/frontend/pkg/config"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

const (
	client_request_id             = "random_client_request_id"
	correlation_request_id string = "random_correlation_request_id"
)

func TestMiddlewareLoggingPostMux(t *testing.T) {
	type testCase struct {
		name   string
		header http.Header
	}

	tt := testCase{
		name: "is able to process and forward the values from request's header to context",
		header: http.Header{
			arm.HeaderNameClientRequestID:      []string{client_request_id},
			arm.HeaderNameCorrelationRequestID: []string{correlation_request_id},
			arm.HeaderNameRequestID:            []string{uuid.NewString()},
		},
	}

	t.Run(tt.name, func(t *testing.T) {
		request, err := http.NewRequest(http.MethodGet, "", nil)
		if err != nil {
			t.Fatal(err)
		}

		request.Header = tt.header

		// we assume the request carries a logger, we set it explicitly to not fail
		ctx := ContextWithLogger(request.Context(), config.DefaultLogger())
		request = request.WithContext(ctx)

		next := func(w http.ResponseWriter, r *http.Request) {
			request = r // capture modified request
			w.WriteHeader(http.StatusOK)
		}

		writer := httptest.NewRecorder()
		MiddlewareLoggingPostMux(writer, request, next)

		result, err := CorrelationDataFromContext(request.Context())
		if err != nil {
			t.Fatal(err)
		}

		if result.ClientRequestID != client_request_id {
			t.Fatalf("ClientRequestID from header was not propperly propagated to requestcontext, expected %v, but got %v",
				client_request_id,
				result.ClientRequestID)
		}
	})

}

// ReqPathModifier is an alias to a function that receives a request
// and it should modify its Path value as needed, for testing purposes.
type ReqPathModifier func(req *http.Request)

// noModifyReqfunc is a function that receives a request and does not modify it.
func noModifyReqfunc(req *http.Request) {
	// empty on purpose
}

func Test_getLogAttrs(t *testing.T) {
	var expectedRequestID = uuid.New()

	fakeSubscriptionId := "the_subscription_id"
	fakeResourceGroupName := "the_resource_group_name"
	fakeResourceName := "the_resource_name"

	sampleCorrelationData := &arm.CorrelationData{
		RequestID:            expectedRequestID,
		ClientRequestID:      client_request_id,
		CorrelationRequestID: correlation_request_id,
		RequestTime:          time.Now(),
	}

	commonAttrs := []slog.Attr{
		slog.String("request_id", expectedRequestID.String()),
		slog.String("client_request_id", client_request_id),
		slog.String("correlation_request_id", correlation_request_id),
	}

	type testCase struct {
		name            string
		correlationData *arm.CorrelationData
		req             *http.Request
		want            []slog.Attr
		setReqPathValue ReqPathModifier
	}

	tests := []testCase{
		{
			name:            "handles the common logging attributes",
			correlationData: sampleCorrelationData,
			req:             &http.Request{},
			want:            commonAttrs,
			setReqPathValue: noModifyReqfunc,
		},
		{
			name:            "handles the common attributes and the attributes for the subscription_id segment path",
			correlationData: sampleCorrelationData,
			req:             &http.Request{},
			want:            append(commonAttrs, slog.String("subscription_id", fakeSubscriptionId)),
			setReqPathValue: func(req *http.Request) {
				req.SetPathValue(PathSegmentSubscriptionID, fakeSubscriptionId)
			},
		},
		{
			name:            "handles the common attributes and the attributes for the resourcegroupname path",
			correlationData: sampleCorrelationData,
			req:             &http.Request{},
			want:            append(commonAttrs, slog.String("resource_group", fakeResourceGroupName)),
			setReqPathValue: func(req *http.Request) {
				req.SetPathValue(PathSegmentResourceGroupName, fakeResourceGroupName)
			},
		},
		{
			name:            "handles the common attributes and the attributes for the resourcegroupname path",
			correlationData: sampleCorrelationData,
			req:             &http.Request{},
			want:            append(commonAttrs, slog.String("resource_group", fakeResourceGroupName)),
			setReqPathValue: func(req *http.Request) {
				req.SetPathValue(PathSegmentResourceGroupName, fakeResourceGroupName)
			},
		},
		{
			name:            "handles the common attributes and the attributes for the resourcename path, and produces the correct resourceID attribute",
			correlationData: sampleCorrelationData,
			req:             &http.Request{},
			want: append(
				commonAttrs,
				slog.String("subscription_id", fakeSubscriptionId),
				slog.String("resource_group", fakeResourceGroupName),
				slog.String("resource_name", fakeResourceName),
				slog.String(
					"resource_id",
					fmt.Sprintf(
						"/subscriptions/%s/resourcegroups/%s/providers/%s/%s",
						fakeSubscriptionId,
						fakeResourceGroupName,
						api.ResourceType,
						fakeResourceName)),
			),
			setReqPathValue: func(req *http.Request) {
				// assuming the PathSegmentResourceName is present in the Path
				req.SetPathValue(PathSegmentResourceName, fakeResourceName)

				// assuming the PathSegmentSubscriptionID is present in the Path
				req.SetPathValue(PathSegmentSubscriptionID, fakeSubscriptionId)

				// assuming the PathSegmentResourceGroupName is present in the Path
				req.SetPathValue(PathSegmentResourceGroupName, fakeResourceGroupName)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setReqPathValue(tt.req)
			got := getLogAttrs(tt.correlationData, tt.req)
			if !reflect.DeepEqual(tt.want, got) {
				t.Errorf("want %v, but got %v", tt.want, got)
			}
		})
	}
}

func Test_setHeaders(t *testing.T) {
	var expectedRequestId = uuid.New()
	const expectedClientRequestId = "the_client_request_id"

	type testCase struct {
		name            string
		w               http.ResponseWriter
		r               *http.Request
		correlationData *arm.CorrelationData
		expectedHeaders http.Header
	}

	tests := []testCase{
		{
			name:            "should set the requestId header to the value of correlation data",
			w:               &httptest.ResponseRecorder{},
			r:               &http.Request{},
			correlationData: &arm.CorrelationData{RequestID: expectedRequestId},
			expectedHeaders: http.Header{
				arm.HeaderNameRequestID: []string{expectedRequestId.String()},
			},
		},
		{
			name: "should set the clientRequestId header to the value of correlation data when the 'should return client request id' header is true",
			w:    &httptest.ResponseRecorder{},
			r: &http.Request{
				Header: http.Header{
					arm.HeaderNameReturnClientRequestID: []string{"true"},
				},
			},
			correlationData: &arm.CorrelationData{
				RequestID:       expectedRequestId,
				ClientRequestID: expectedClientRequestId,
			},
			expectedHeaders: http.Header{
				arm.HeaderNameRequestID:       []string{expectedRequestId.String()},
				arm.HeaderNameClientRequestID: []string{expectedClientRequestId},
			},
		},
		{
			name: "should not set the clientRequestId header to the value of correlation data when the 'should return client request id' header is false",
			w:    &httptest.ResponseRecorder{},
			r: &http.Request{
				Header: http.Header{
					arm.HeaderNameReturnClientRequestID: []string{"false"},
				},
			},
			correlationData: &arm.CorrelationData{
				RequestID:       expectedRequestId,
				ClientRequestID: expectedClientRequestId,
			},
			expectedHeaders: http.Header{
				arm.HeaderNameRequestID: []string{expectedRequestId.String()},
			},
		},
		{
			name: "should not set the clientRequestId header to the value from correlation data when header is empty",
			w:    &httptest.ResponseRecorder{},
			r:    &http.Request{},
			correlationData: &arm.CorrelationData{
				RequestID:       expectedRequestId,
				ClientRequestID: expectedClientRequestId,
			},
			expectedHeaders: http.Header{
				arm.HeaderNameRequestID: []string{expectedRequestId.String()},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setHeaders(tt.w, tt.r, tt.correlationData)
			assertAllHeadersAreWritten(t, tt.expectedHeaders, tt.w)
		})
	}
}

// assertAllHeadersAreWritten asserts that all the headers h are written in w
func assertAllHeadersAreWritten(t *testing.T, h http.Header, w http.ResponseWriter) {
	for expectedKey, expectedValues := range h {
		valueInHeader := w.Header().Get(expectedKey)
		if valueInHeader == "" {
			t.Fatalf("header with key %v is not present in response writer\n", expectedKey)
		}

		if valueInHeader != expectedValues[0] {
			t.Fatalf("header with key %v and value %v is different than expected value %v in response writer\n", expectedKey, valueInHeader, expectedValues[0])
		}
	}
}
