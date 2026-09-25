// Copyright 2026 Microsoft Corporation
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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"testing/synctest"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20240610preview"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestPhaseTimerHistogramAndLogs(t *testing.T) {
	methodPattern, methodlessPattern := "GET /resources/{name}", "/resources/{name}"
	for _, tt := range []struct {
		name    string
		pattern *string
		route   string
	}{
		{name: "missing pattern", route: "<not routed>"},
		{name: "empty pattern", pattern: new(string), route: "<not routed>"},
		{name: "method pattern", pattern: &methodPattern, route: "/resources/{name}"},
		{name: "methodless pattern", pattern: &methodlessPattern, route: "/resources/{name}"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Fake time stays fixed while the test runs, making bucket boundaries exact.
			synctest.Test(t, func(t *testing.T) {
				reg := prometheus.NewRegistry()
				mm := NewMetricsMiddleware(reg)
				var logs bytes.Buffer
				ctx := utils.ContextWithLogger(t.Context(), logr.FromSlogHandler(slog.NewJSONHandler(&logs, nil)))
				if tt.pattern != nil {
					ctx = ContextWithPattern(ctx, tt.pattern)
				}
				req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/resources/private-name?api-version=private-version", nil)
				var durations []float64
				mm.Metrics()(httptest.NewRecorder(), req, func(w http.ResponseWriter, r *http.Request) {
					require.Empty(t, gatherPhaseMetrics(t, reg), "middleware must not pre-create phase observations")
					for _, elapsed := range []time.Duration{0, 100 * time.Microsecond, 300 * time.Microsecond, 750 * time.Millisecond, 1125 * time.Millisecond, 20 * time.Second} {
						before := gatherPhaseMetrics(t, reg)
						logLength := logs.Len()
						timer := startPhase(r.Context(), PhaseResourceRead)
						timer.start = time.Now().Add(-elapsed)
						require.Equal(t, before, gatherPhaseMetrics(t, reg), "starting a phase must not observe it")
						require.Equal(t, logLength, logs.Len(), "starting a phase must not log completion")
						timer.End()
						durations = append(durations, elapsed.Seconds())
					}
					w.WriteHeader(http.StatusOK)
				})

				metrics := gatherPhaseMetrics(t, reg)
				require.Len(t, metrics, 1)
				metric := metrics[0]
				require.Len(t, metric.GetLabel(), 3, "phase labels must remain low-cardinality")
				assert.Equal(t, tt.route, labelValue(t, metric, "route"))
				assert.Equal(t, http.MethodGet, labelValue(t, metric, "method"))
				assert.Equal(t, "resource_read", labelValue(t, metric, "phase"))
				histogram := metric.GetHistogram()
				require.NotNil(t, histogram)
				assert.Equal(t, uint64(len(durations)), histogram.GetSampleCount())
				var sum float64
				for _, duration := range durations {
					sum += duration
				}
				assert.Equal(t, sum, histogram.GetSampleSum())
				bounds := []float64{0.0001, 0.0005, 0.001, 0.005, 0.025, 0.05, 0.1, 0.2, 0.4, 0.6, 0.8, 1, 1.25, 1.5, 2, 3, 4, 5, 6, 8, 10, 15}
				require.Len(t, histogram.GetBucket(), len(bounds))
				for i, bound := range bounds {
					var count uint64
					for _, duration := range durations {
						if duration <= bound {
							count++
						}
					}
					assert.Equal(t, bound, histogram.GetBucket()[i].GetUpperBound())
					assert.Equal(t, count, histogram.GetBucket()[i].GetCumulativeCount(), "bucket %g", bound)
				}
				entries := phaseLogEntries(t, &logs)
				require.Len(t, entries, len(durations))
				for i, entry := range entries {
					assert.Equal(t, "resource_read", entry["phase"])
					assert.Equal(t, tt.route, entry["route"])
					assert.Equal(t, durations[i], entry["duration_seconds"])
					assert.NotContains(t, entry, "outcome")
				}
				lintMetrics(t, reg)
			})
		})
	}
}

func TestPhaseTimerWithoutMetrics(t *testing.T) {
	var logs bytes.Buffer
	ctx := utils.ContextWithLogger(t.Context(), logr.FromSlogHandler(slog.NewJSONHandler(&logs, nil)))
	timer := startPhase(ctx, PhaseResourceRead)
	require.NotPanics(t, timer.End)
	require.Empty(t, logs.String(), "helpers outside the request pipeline must not emit phase logs")
}

func TestPhasePostMuxRouteAndCorrelation(t *testing.T) {
	type copyKey struct{}
	reg := prometheus.NewRegistry()
	mm := NewMetricsMiddleware(reg)
	var logs bytes.Buffer
	ctx := utils.ContextWithLogger(t.Context(), logr.FromSlogHandler(slog.NewJSONHandler(&logs, nil)))
	pattern := MuxPattern(http.MethodPut, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters)
	route := strings.TrimPrefix(pattern, http.MethodPut+" ")
	var beforeMux *http.Request
	var beforeMuxTimer phaseTimer
	mux := NewMiddlewareMux(mm.Metrics(), MiddlewareCorrelationData, MiddlewareLogging, MiddlewareLowercase, MiddlewareBody,
		func(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
			beforeMux = r
			beforeMuxTimer = startPhase(r.Context(), PhaseResourceRead)
			next(w, r.WithContext(context.WithValue(r.Context(), copyKey{}, "copy")))
		})
	postMux := NewMiddleware(MiddlewareLoggingPostMux, func(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
		// This timer retained a pre-mux context, not the matched Request copy.
		beforeMuxTimer.End()
		timer := startPhase(r.Context(), PhaseAdmission)
		timer.End()
		next(w, r.WithContext(context.WithValue(r.Context(), copyKey{}, "another copy")))
	})
	want := map[phaseName]string{PhaseBodyRead: "<not routed>", PhaseResourceRead: route, PhaseAdmission: route}
	called := false
	mux.Handle(pattern, postMux.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		require.Empty(t, beforeMux.Pattern, "ServeMux matched a WithContext copy")
		require.Equal(t, pattern, r.Pattern)
		require.Equal(t, pattern, *PatternFromContext(beforeMux.Context()))
		requirePhaseObservations(t, reg, http.MethodPut, want)
		require.Len(t, phaseLogEntries(t, &logs), 3, "phases must emit before the handler returns")
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequestWithContext(ctx, http.MethodPut, coreapitesting.TestClusterResourceID, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(coreapi.HeaderNameClientRequestID, "phase-client-request")
	req.Header.Set(coreapi.HeaderNameCorrelationRequestID, "phase-correlation-request")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	require.True(t, called)
	require.Equal(t, http.StatusOK, response.Code)
	observations := requirePhaseObservations(t, reg, http.MethodPut, want)
	requestID := response.Header().Get(coreapi.HeaderNameRequestID)
	require.NotEmpty(t, requestID)
	for _, entry := range phaseLogEntries(t, &logs) {
		phase := phaseName(entry["phase"].(string))
		assert.Equal(t, want[phase], entry["route"])
		assert.Equal(t, observations[phase].GetSampleSum(), entry["duration_seconds"])
		assert.Equal(t, requestID, entry["request_id"])
		assert.Equal(t, "phase-client-request", entry["client_request_id"])
		assert.Equal(t, "phase-correlation-request", entry["correlation_request_id"])
		assert.Equal(t, strings.ToLower(coreapitesting.TestClusterResourceID), entry["resource_id"])
		assert.Equal(t, "put", entry["method"])
	}
}

func TestPhaseBodyReadRejection(t *testing.T) {
	for _, tt := range []struct {
		name        string
		body        io.Reader
		contentType string
		status      int
	}{
		{name: "read error", body: iotest.ErrReader(errors.New("body read failed")), contentType: "application/json", status: http.StatusBadRequest},
		{name: "oversized", body: strings.NewReader(strings.Repeat("x", int(4*megabyte+1))), contentType: "application/json", status: http.StatusBadRequest},
		{name: "unsupported media type", body: strings.NewReader(`{}`), contentType: "text/plain", status: http.StatusUnsupportedMediaType},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			var logs bytes.Buffer
			ctx := utils.ContextWithLogger(t.Context(), logr.FromSlogHandler(slog.NewJSONHandler(&logs, nil)))
			mux := NewMiddlewareMux(NewMetricsMiddleware(reg).Metrics(), MiddlewareCorrelationData, MiddlewareBody)
			mux.Handle("PUT /resources/{name}", NewMiddleware().HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("body rejection must not reach the route handler")
			}))
			req := httptest.NewRequestWithContext(ctx, http.MethodPut, "/resources/private-name", tt.body)
			req.Header.Set("Content-Type", tt.contentType)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, req)
			require.Equal(t, tt.status, response.Code)
			observations := requirePhaseObservations(t, reg, http.MethodPut, map[phaseName]string{PhaseBodyRead: "<not routed>"})
			entries := phaseLogEntries(t, &logs)
			require.Len(t, entries, 1)
			assert.Equal(t, "body_read", entries[0]["phase"])
			assert.Equal(t, "<not routed>", entries[0]["route"])
			assert.Equal(t, response.Header().Get(coreapi.HeaderNameRequestID), entries[0]["request_id"])
			assert.Equal(t, observations[PhaseBodyRead].GetSampleSum(), entries[0]["duration_seconds"])
		})
	}
}

func TestPhaseAPIVersionValidationEndsBeforeNext(t *testing.T) {
	for _, version := range []string{coreapitesting.TestAPIVersion, "", "1999-12-31"} {
		t.Run("version="+version, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			apiRegistry := coreapi.NewAPIRegistry()
			require.NoError(t, v20240610preview.RegisterVersion(apiRegistry))
			mux := NewMiddlewareMux(NewMetricsMiddleware(reg).Metrics())
			want := map[phaseName]string{PhaseAPIVersionValidation: "/resources/{name}"}
			var atNext map[phaseName]*dto.Histogram
			mux.Handle("GET /resources/{name}", NewMiddleware(newMiddlewareValidatedAPIVersion(apiRegistry).handleRequest).HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atNext = requirePhaseObservations(t, reg, http.MethodGet, want)
				_, err := VersionFromContext(r.Context())
				require.NoError(t, err)
				w.WriteHeader(http.StatusOK)
			}))
			response := httptest.NewRecorder()
			ctx := utils.ContextWithLogger(t.Context(), logr.Discard())
			mux.ServeHTTP(response, httptest.NewRequestWithContext(ctx, http.MethodGet, "/resources/private-name?api-version="+version, nil))
			after := requirePhaseObservations(t, reg, http.MethodGet, want)
			if version == coreapitesting.TestAPIVersion {
				require.Equal(t, http.StatusOK, response.Code)
				require.NotNil(t, atNext, "next must be called")
				require.Equal(t, atNext, after, "downstream work must not change the observation")
			} else {
				require.Equal(t, http.StatusBadRequest, response.Code)
				require.Nil(t, atNext, "invalid versions must not reach next")
			}
		})
	}
}

func TestPhaseSubscriptionValidationEndsBeforeNext(t *testing.T) {
	for _, tt := range []struct {
		name   string
		state  coreapi.SubscriptionState
		method string
		status int
	}{
		{name: "registered", state: coreapi.SubscriptionStateRegistered, method: http.MethodPut, status: http.StatusOK},
		{name: "warned read", state: coreapi.SubscriptionStateWarned, method: http.MethodGet, status: http.StatusOK},
		{name: "suspended delete", state: coreapi.SubscriptionStateSuspended, method: http.MethodDelete, status: http.StatusOK},
		{name: "warned write", state: coreapi.SubscriptionStateWarned, method: http.MethodPut, status: http.StatusConflict},
		{name: "unregistered", state: coreapi.SubscriptionStateUnregistered, method: http.MethodGet, status: http.StatusBadRequest},
		{name: "deleted", state: coreapi.SubscriptionStateDeleted, method: http.MethodGet, status: http.StatusBadRequest},
		{name: "unknown state", state: "unknown", method: http.MethodGet, status: http.StatusInternalServerError},
		{name: "not found", method: http.MethodGet, status: http.StatusBadRequest},
		{name: "missing subscription ID", method: http.MethodGet, status: http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			if tt.state != "" {
				_, err := db.Subscriptions().Create(t.Context(), newTestSubscription(coreapitesting.TestSubscriptionID, tt.state, nil), nil)
				require.NoError(t, err)
			}
			pattern := MuxPattern(tt.method, PatternSubscriptions)
			url := coreapitesting.TestSubscriptionResourceID
			if tt.name == "missing subscription ID" {
				pattern = tt.method + " /missing"
				url = "/missing"
			}
			route := strings.TrimPrefix(pattern, tt.method+" ")
			want := map[phaseName]string{PhaseSubscriptionValidation: route}
			if tt.name != "missing subscription ID" {
				want[PhaseSubscriptionValidationRead] = route
			}
			mux := NewMiddlewareMux(NewMetricsMiddleware(reg).Metrics())
			var atNext map[phaseName]*dto.Histogram
			mux.Handle(pattern, NewMiddleware(newMiddlewareValidateSubscriptionState(db).handleRequest).HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atNext = requirePhaseObservations(t, reg, tt.method, want)
				subscription, err := SubscriptionFromContext(r.Context())
				require.NoError(t, err)
				require.Equal(t, tt.state, subscription.State)
				w.WriteHeader(http.StatusOK)
			}))
			response := httptest.NewRecorder()
			ctx := utils.ContextWithLogger(t.Context(), logr.Discard())
			mux.ServeHTTP(response, httptest.NewRequestWithContext(ctx, tt.method, url, nil))
			require.Equal(t, tt.status, response.Code)
			after := requirePhaseObservations(t, reg, tt.method, want)
			if tt.status == http.StatusOK {
				require.NotNil(t, atNext, "next must be called")
				require.Equal(t, atNext, after, "downstream work must not change either validation observation")
			} else {
				require.Nil(t, atNext, "rejected subscriptions must not reach next")
			}
			if read := after[PhaseSubscriptionValidationRead]; read != nil {
				assert.GreaterOrEqual(t, after[PhaseSubscriptionValidation].GetSampleSum(), read.GetSampleSum(), "parent phase includes its read child")
			}
		})
	}
}

func TestPhaseFrontendResourceRoutes(t *testing.T) {
	clusterSegments := []string{PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters}
	for _, resource := range []struct {
		name     string
		id       string
		segments []string
	}{
		{name: "cluster", id: coreapitesting.TestClusterResourceID, segments: clusterSegments},
		{name: "node pool", id: coreapitesting.TestNodePoolResourceID, segments: append(append([]string{}, clusterSegments...), PatternNodePools)},
		{name: "external auth", id: coreapitesting.TestExternalAuthResourceID, segments: append(append([]string{}, clusterSegments...), PatternExternalAuth)},
	} {
		for _, operation := range []struct {
			name   string
			method string
			items  int
			list   bool
		}{
			{name: "read", method: http.MethodGet, items: 1},
			{name: "empty list", method: http.MethodGet, list: true},
			{name: "multi-item list", method: http.MethodGet, items: 3, list: true},
			{name: "invalid create body", method: http.MethodPut},
			{name: "missing delete", method: http.MethodDelete},
		} {
			t.Run(resource.name+"/"+operation.name, func(t *testing.T) {
				reg := prometheus.NewRegistry()
				db := corecosmosstoragetesting.NewMockResourcesDBClient()
				var logs bytes.Buffer
				logger := logr.FromSlogHandler(slog.NewJSONHandler(&logs, nil))
				ctx := utils.ContextWithLogger(t.Context(), logger)
				f := NewFrontend(logger, nil, nil, reg, reg, db, nil, &testClient{}, coreapitesting.TestLocation, false)
				_, err := db.Subscriptions().Create(ctx, newTestSubscription(coreapitesting.TestSubscriptionID, coreapi.SubscriptionStateRegistered, nil), nil)
				require.NoError(t, err)
				clusters := db.HCPClusters(coreapitesting.TestSubscriptionID, coreapitesting.TestResourceGroupName)
				if resource.name != "cluster" {
					parent := coreapitesting.MinimumValidClusterTestCase()
					parent.ResourceID = parent.ID
					parent.PartitionKey = coreapitesting.TestSubscriptionID
					_, err = clusters.Create(ctx, parent, nil)
					require.NoError(t, err)
				}
				for i := range operation.items {
					id := resource.id
					if i > 0 {
						id += fmt.Sprintf("-%d", i)
					}
					resourceID, err := azcorearm.ParseResourceID(id)
					require.NoError(t, err)
					switch resource.name {
					case "cluster":
						cluster := coreapi.NewDefaultHCPOpenShiftCluster(resourceID, coreapitesting.TestLocation)
						cluster.ResourceID = resourceID
						cluster.PartitionKey = coreapitesting.TestSubscriptionID
						_, err = clusters.Create(ctx, cluster, nil)
					case "node pool":
						nodePool := coreapi.NewDefaultHCPOpenShiftClusterNodePool(resourceID, coreapitesting.TestLocation)
						nodePool.ResourceID = resourceID
						nodePool.PartitionKey = coreapitesting.TestSubscriptionID
						_, err = clusters.NodePools(coreapitesting.TestClusterName).Create(ctx, nodePool, nil)
					case "external auth":
						externalAuth := coreapi.NewDefaultHCPOpenShiftClusterExternalAuth(resourceID)
						externalAuth.ResourceID = resourceID
						externalAuth.PartitionKey = coreapitesting.TestSubscriptionID
						_, err = clusters.ExternalAuth(coreapitesting.TestClusterName).Create(ctx, externalAuth, nil)
					}
					require.NoError(t, err)
				}
				url := resource.id
				route := strings.TrimPrefix(MuxPattern(operation.method, resource.segments...), operation.method+" ")
				if operation.list {
					url = url[:strings.LastIndex(url, "/")]
					route = route[:strings.LastIndex(route, "/")]
				}
				var body io.Reader
				if operation.method == http.MethodPut {
					body = strings.NewReader(`{"properties":`)
				}
				req := httptest.NewRequestWithContext(ctx, operation.method, url+"?api-version="+coreapitesting.TestAPIVersion, body)
				if body != nil {
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set(coreapi.HeaderNameARMResourceSystemData, `{}`)
				}
				response := httptest.NewRecorder()
				f.server.Handler.ServeHTTP(response, req)
				want := map[phaseName]string{
					PhaseAPIVersionValidation:       route,
					PhaseSubscriptionValidation:     route,
					PhaseSubscriptionValidationRead: route,
					PhaseAuditSend:                  route,
				}
				status := http.StatusOK
				switch {
				case operation.list:
					want[PhaseResourceList] = route
					want[PhaseResponseWrite] = route
					if resource.name != "cluster" {
						want[PhaseResourceRead] = route
					}
					var page coreapi.PagedResponse
					require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page), "%s", response.Body.String())
					require.Len(t, page.Value, operation.items, "the loop must actually process the seeded items")
				case operation.method == http.MethodGet:
					want[PhaseResourceRead] = route
					want[PhaseResponseEncode] = route
					want[PhaseResponseWrite] = route
				case operation.method == http.MethodPut:
					status = http.StatusBadRequest
					// The outer mux's catch-all is not an ARM route match.
					want[PhaseBodyRead] = "<not routed>"
					want[PhaseResourceRead] = route
					want[PhaseDecode] = route
				case operation.method == http.MethodDelete:
					status = http.StatusNoContent
					want[PhaseResourceRead] = route
					want[PhaseResponseWrite] = route
				}
				require.Equal(t, status, response.Code, "%s", response.Body.String())
				observations := requirePhaseObservations(t, reg, operation.method, want)
				entries := phaseLogEntries(t, &logs)
				require.Len(t, entries, len(want), "phase log volume must not grow with list length")
				seen := map[phaseName]bool{}
				for _, entry := range entries {
					phase := phaseName(entry["phase"].(string))
					require.Contains(t, observations, phase)
					require.False(t, seen[phase], "duplicate phase log %s", phase)
					seen[phase] = true
					assert.Equal(t, want[phase], entry["route"])
					assert.Equal(t, observations[phase].GetSampleSum(), entry["duration_seconds"])
					assert.Equal(t, response.Header().Get(coreapi.HeaderNameRequestID), entry["request_id"])
				}
			})
		}
	}
}

func gatherPhaseMetrics(t *testing.T, reg prometheus.Gatherer) []*dto.Metric {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() == "frontend_http_request_phase_duration_seconds" {
			require.Equal(t, dto.MetricType_HISTOGRAM, family.GetType())
			return family.GetMetric()
		}
	}
	return nil
}

func requirePhaseObservations(t *testing.T, reg prometheus.Gatherer, method string, want map[phaseName]string) map[phaseName]*dto.Histogram {
	t.Helper()
	metrics := gatherPhaseMetrics(t, reg)
	require.Len(t, metrics, len(want), "unexpected phase series")
	observations := make(map[phaseName]*dto.Histogram, len(metrics))
	for _, metric := range metrics {
		require.Len(t, metric.GetLabel(), 3, "only route, method and phase labels are allowed")
		phase := phaseName(labelValue(t, metric, "phase"))
		route, ok := want[phase]
		require.True(t, ok, "unexpected phase %q", phase)
		require.NotContains(t, observations, phase, "duplicate phase series")
		require.Equal(t, route, labelValue(t, metric, "route"), "phase %s", phase)
		require.Equal(t, method, labelValue(t, metric, "method"), "phase %s", phase)
		histogram := metric.GetHistogram()
		require.NotNil(t, histogram)
		require.Equal(t, uint64(1), histogram.GetSampleCount(), "phase %s must emit once per request, not per item", phase)
		observations[phase] = histogram
	}
	return observations
}

func phaseLogEntries(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var entries []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(logs.Bytes()))
	for {
		var entry map[string]any
		err := decoder.Decode(&entry)
		if err == io.EOF {
			return entries
		}
		require.NoError(t, err)
		if entry["msg"] == "request phase complete" {
			entries = append(entries, entry)
		}
	}
}
