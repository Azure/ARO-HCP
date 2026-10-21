// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package frontend

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestMiddlewarePanic_middlewarePanicRecover(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                 string
		muxPatternForContext string
		clearRequestMethod   bool
		handler              http.HandlerFunc
		wantStatus           int
		wantCounterRoute     string
		wantCounterMethod    string
		wantCounterDelta     float64
	}{
		{
			name:                 "a panic increments the counter",
			muxPatternForContext: "/panic/table/panic_strips_method_and_increments",
			handler: func(http.ResponseWriter, *http.Request) {
				panic("test panic")
			},
			wantStatus:        http.StatusInternalServerError,
			wantCounterRoute:  "/panic/table/panic_strips_method_and_increments",
			wantCounterMethod: http.MethodGet,
			wantCounterDelta:  1,
		},
		{
			name:                 "no panic does not increment the counter",
			muxPatternForContext: "/panic/table/no_panic_does_not_increment",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTeapot)
			},
			wantStatus:        http.StatusTeapot,
			wantCounterRoute:  "/panic/table/no_panic_does_not_increment",
			wantCounterMethod: http.MethodGet,
			wantCounterDelta:  0,
		},
		{
			name:                 "when a panic occurs and no pattern is set, the counter is incremented with the unmatched route label",
			muxPatternForContext: "",
			handler: func(http.ResponseWriter, *http.Request) {
				panic("test panic")
			},
			wantStatus:        http.StatusInternalServerError,
			wantCounterRoute:  "unmatched",
			wantCounterMethod: http.MethodGet,
			wantCounterDelta:  1,
		},
		{
			name:                 "when a panic occurs and the request method is empty, the counter is incremented with the unknown method label",
			muxPatternForContext: "/panic/table/panic_empty_req_method_sets_unknown_metric_label",
			clearRequestMethod:   true,
			handler: func(http.ResponseWriter, *http.Request) {
				panic("test panic")
			},
			wantStatus:        http.StatusInternalServerError,
			wantCounterRoute:  "/panic/table/panic_empty_req_method_sets_unknown_metric_label",
			wantCounterMethod: "unknown",
			wantCounterDelta:  1,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			methodGET := http.MethodGet
			muxPat := tt.muxPatternForContext
			if muxPat != "" {
				muxPat = fmt.Sprintf("%s %s", methodGET, muxPat)
			}

			req := httptest.NewRequest(methodGET, "/unused", nil)
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			if muxPat != "" {
				req = req.WithContext(ContextWithPattern(ctx, &muxPat))
			} else {
				req = req.WithContext(ctx)
			}
			if tt.clearRequestMethod {
				req.Method = ""
			}

			counter := frontendHTTPRequestPanicsTotalCounterVec.WithLabelValues(tt.wantCounterRoute, tt.wantCounterMethod)
			before := testutil.ToFloat64(counter)

			rec := httptest.NewRecorder()
			middlewarePanicRecover(rec, req, tt.handler, true)

			require.Equal(t, tt.wantStatus, rec.Code)
			require.Equal(t, tt.wantCounterDelta, testutil.ToFloat64(counter)-before,
				"counter delta for route=%q method=%q", tt.wantCounterRoute, tt.wantCounterMethod,
			)
		})
	}
}

func TestMiddlewarePanic_middlewarePanicRecover_recoverDisabled_propagatesPanic(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(utils.ContextWithLogger(context.Background(), testr.New(t)))
	rec := httptest.NewRecorder()

	require.Panics(t, func() {
		middlewarePanicRecover(rec, req, func(http.ResponseWriter, *http.Request) {
			panic("test panic")
		}, false)
	})
}
