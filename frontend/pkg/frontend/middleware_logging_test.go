package frontend

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api"
)

// ReqPathModifier is an alias to a function that receives a request
// and it should modify its Path value as needed, for testing purposes.
type ReqPathModifier func(req *http.Request)

// noModifyReqfunc is a function that receives a request and does not modify it.
func noModifyReqfunc(req *http.Request) {
	// empty on purpose
}

func TestMiddlewareLoggingPostMux(t *testing.T) {
	fakeSubscriptionId := "the_subscription_id"
	fakeResourceGroupName := "the_resource_group_name"
	fakeResourceName := "the_resource_name"

	type testCase struct {
		name            string
		wantLogAttrs    []slog.Attr
		wantSpanAttrs   map[string]string
		setReqPathValue ReqPathModifier
	}

	tests := []testCase{
		{
			name:            "handles the common logging attributes",
			wantLogAttrs:    []slog.Attr{},
			setReqPathValue: noModifyReqfunc,
		},
		{
			name:          "handles the common attributes and the attributes for the subscription_id segment path",
			wantLogAttrs:  []slog.Attr{slog.String("subscription_id", fakeSubscriptionId)},
			wantSpanAttrs: map[string]string{"aro.subscription.id": fakeSubscriptionId},
			setReqPathValue: func(req *http.Request) {
				req.SetPathValue(PathSegmentSubscriptionID, fakeSubscriptionId)
			},
		},
		{
			name:          "handles the common attributes and the attributes for the resourcegroupname path",
			wantLogAttrs:  []slog.Attr{slog.String("resource_group", fakeResourceGroupName)},
			wantSpanAttrs: map[string]string{"aro.resource_group.name": fakeResourceGroupName},
			setReqPathValue: func(req *http.Request) {
				req.SetPathValue(PathSegmentResourceGroupName, fakeResourceGroupName)
			},
		},
		{
			name: "handles the common attributes and the attributes for the resourcename path, and produces the correct resourceID attribute",
			wantLogAttrs: []slog.Attr{
				slog.String("subscription_id", fakeSubscriptionId),
				slog.String("resource_group", fakeResourceGroupName),
				slog.String("resource_name", fakeResourceName),
				slog.String(
					"resource_id",
					fmt.Sprintf(
						"/subscriptions/%s/resourcegroups/%s/providers/%s/%s",
						fakeSubscriptionId,
						fakeResourceGroupName,
						api.ClusterResourceType,
						fakeResourceName)),
			},
			wantSpanAttrs: map[string]string{
				"aro.subscription.id":     fakeSubscriptionId,
				"aro.resource_group.name": fakeResourceGroupName,
				"aro.resource.name":       fakeResourceName,
			},
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
			var (
				writer = httptest.NewRecorder()
				buf    bytes.Buffer
				logger = slog.New(slog.NewTextHandler(&buf, nil))
			)

			ctx := ContextWithLogger(context.Background(), logger)
			ctx, sr := initSpanRecorder(ctx)
			req, err := http.NewRequestWithContext(ctx, "GET", "http://example.com", nil)
			assert.NoError(t, err)
			tt.setReqPathValue(req)

			next := func(w http.ResponseWriter, r *http.Request) {
				logger := LoggerFromContext(r.Context())
				// Emit a log message to check that it includes the expected attributes.
				logger.Info("test")
				w.WriteHeader(http.StatusOK)
			}

			MiddlewareLoggingPostMux(writer, req, next)

			// Check that the contextual logger has the expected attributes.
			lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
			require.Equal(t, 1, len(lines))

			line := string(lines[0])
			for _, attr := range tt.wantLogAttrs {
				assert.Contains(t, line, attr.String())
			}

			// Check that the attributes have been added to the span too.
			ss := sr.collect()
			require.Len(t, ss, 1)
			span := ss[0]
			equalSpanAttributes(t, span, tt.wantSpanAttrs)
		})
	}
}
