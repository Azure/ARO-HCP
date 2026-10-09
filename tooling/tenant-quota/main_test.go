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

package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/cijoboutcomes"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
	prowmetrics "github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/prow"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/prowjobs"
)

type prowClientFunc func(context.Context) ([]prowjobs.Job, error)

func (f prowClientFunc) ListJobs(ctx context.Context) ([]prowjobs.Job, error) {
	return f(ctx)
}

func TestCollectorRestartsAfterRecoveredPanic(t *testing.T) {
	originalCrash := utilruntime.ReallyCrash
	utilruntime.ReallyCrash = false
	defer func() { utilruntime.ReallyCrash = originalCrash }()
	cfg := &config.Config{
		Tenants: []config.TenantConfig{{TenantID: "tenant", ServicePrincipalClientId: "client", KeyVaultSecretName: "secret"}},
		Prow: config.ProwConfig{
			Enabled: true, Interval: "1h", BaseURL: "https://prow.example.com",
			Repository: config.ProwRepositoryConfig{Org: "Azure", Name: "ARO-HCP"},
		},
	}
	require.NoError(t, cfg.Validate())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var calls int
	var firstAttempt, secondAttempt time.Time
	collector := prowmetrics.NewCollector(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), prowClientFunc(func(context.Context) ([]prowjobs.Job, error) {
		calls++
		if calls == 1 {
			firstAttempt = time.Now()
			panic("collection failed")
		}
		secondAttempt = time.Now()
		cancel()
		return nil, nil
	}))
	runCollector(ctx, collector.Start)
	require.Equal(t, 2, calls, "a recovered panic must not permanently stop collection")
	require.GreaterOrEqual(t, secondAttempt.Sub(firstAttempt), time.Second, "restart must back off rather than spin")
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)
	metrics, err := registry.Gather()
	require.NoError(t, err)
	for _, metric := range metrics {
		if metric.GetName() == "prow_ci_collection_success" {
			require.Equal(t, float64(1), metric.Metric[0].GetGauge().GetValue(), "restarted collector must publish fresh metrics")
			return
		}
	}
	t.Fatal("collection success metric missing")
}

func TestCollectorCancellationDuringRestartDelay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan struct{})
	finished := make(chan struct{})
	var calls int
	go func() {
		defer close(finished)
		runCollector(ctx, func(context.Context) {
			calls++
			if calls == 1 {
				close(returned)
			}
		})
	}()
	<-returned
	cancel()
	select {
	case <-finished:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("cancellation did not interrupt collector restart delay")
	}
	require.Equal(t, 1, calls, "cancelled collectors must not restart")
	runCollector(ctx, func(context.Context) { t.Fatal("already cancelled collector started") })
}

func TestPanicPolicy(t *testing.T) {
	originalCrash, originalHandlers := utilruntime.ReallyCrash, utilruntime.PanicHandlers
	t.Cleanup(func() {
		utilruntime.ReallyCrash, utilruntime.PanicHandlers = originalCrash, originalHandlers
		panicMetricsOnce = sync.Once{}
	})
	for _, tc := range []struct {
		name, yaml string
		fatal      bool
	}{
		{name: "default"},
		{name: "explicit false", yaml: "exitOnPanic: false"},
		{name: "explicit true", yaml: "exitOnPanic: true", fatal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg config.Config
			require.NoError(t, yaml.Unmarshal([]byte(tc.yaml), &cfg))
			configurePanicHandling(cfg.ExitOnPanic)
			configurePanicHandling(cfg.ExitOnPanic)
			require.Equal(t, tc.fatal, utilruntime.ReallyCrash)
			require.Len(t, utilruntime.PanicHandlers, len(originalHandlers)+1)
			for i, handler := range originalHandlers {
				require.Equal(t, reflect.ValueOf(handler).Pointer(), reflect.ValueOf(utilruntime.PanicHandlers[i]).Pointer(), "existing panic logging must remain installed")
			}

			var logs bytes.Buffer
			ctx := utils.ContextWithLogger(context.Background(), logr.FromSlogHandler(slog.NewJSONHandler(&logs, nil)))
			ctx = utils.ContextWithControllerName(ctx, t.Name())
			counter := utils.PanicTotal.WithLabelValues(t.Name())
			before := testutil.ToFloat64(counter)
			panicOnce := func() {
				defer utilruntime.HandleCrashWithContext(ctx)
				panic("test panic policy")
			}
			if tc.fatal {
				require.PanicsWithValue(t, "test panic policy", panicOnce)
			} else {
				require.NotPanics(t, panicOnce)
			}
			require.Equal(t, before+1, testutil.ToFloat64(counter), "each panic must be counted exactly once")
			require.Contains(t, logs.String(), "Observed a panic")
		})
	}
}

func TestMetricsEndpoint(t *testing.T) {
	registry := prometheus.NewRegistry()
	privateMetric := prometheus.NewGauge(prometheus.GaugeOpts{Name: "tenant_quota_endpoint_test", Help: "Private registry test."})
	registry.MustRegister(privateMetric)
	privateMetric.Set(42)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	writer := cijoboutcomes.NewWriter(&config.Config{}, logger)
	writer.RegisterMetrics(registry)
	// A cancelled Start shuts down the real controller queue without Azure calls.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	writer.Start(ctx)

	queue := workqueue.NewTypedRateLimitingQueueWithConfig(
		workqueue.NewTypedItemExponentialFailureRateLimiter[string](time.Millisecond, time.Second),
		workqueue.TypedRateLimitingQueueConfig[string]{Name: t.Name()},
	)
	defer queue.ShutDown()
	queue.Add("build")
	item, shutdown := queue.Get()
	require.False(t, shutdown)
	queue.Done(item)
	queue.AddRateLimited(item)
	utils.PanicTotal.WithLabelValues(t.Name()).Inc()

	server := httptest.NewServer(newHTTPHandler(registry))
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/metrics")
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode, string(body))
	for _, metric := range []string{
		"tenant_quota_endpoint_test 42",
		`ci_job_outcomes_cache_size{cache="batches"} 0`,
		`workqueue_depth{name="ci-job-outcomes"}`,
		`workqueue_adds_total{name="TestMetricsEndpoint"}`,
		`workqueue_retries_total{name="TestMetricsEndpoint"}`,
		`workqueue_queue_duration_seconds_count{name="TestMetricsEndpoint"}`,
		`workqueue_work_duration_seconds_count{name="TestMetricsEndpoint"}`,
		`panic_total{controller="TestMetricsEndpoint"}`,
		"go_goroutines ",
	} {
		require.Contains(t, string(body), metric)
	}
}

func TestServeShutdown(t *testing.T) {
	for _, failHTTP := range []bool{false, true} {
		name := "cancellation"
		if failHTTP {
			name = "HTTP failure"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			controllerCancelled := make(chan struct{})
			controllerRelease := make(chan struct{})
			defer func() {
				select {
				case <-controllerRelease:
				default:
					close(controllerRelease)
				}
			}()
			startController := func(ctx context.Context) {
				<-ctx.Done()
				close(controllerCancelled)
				<-controllerRelease
			}
			address := make(chan string, 1)
			requestStarted := make(chan struct{})
			requestRelease := make(chan struct{})
			defer func() {
				select {
				case <-requestRelease:
				default:
					close(requestRelease)
				}
			}()
			server := &http.Server{
				Addr: "127.0.0.1:0",
				BaseContext: func(listener net.Listener) context.Context {
					address <- listener.Addr().String()
					return context.Background()
				},
				Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					close(requestStarted)
					<-requestRelease
					w.WriteHeader(http.StatusOK)
				}),
			}
			defer server.Close()
			if failHTTP {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, err)
				defer listener.Close()
				server.Addr = listener.Addr().String()
			}
			finished := make(chan error, 1)
			go func() { finished <- serve(ctx, cancel, logger, server, startController) }()
			if !failHTTP {
				var addr string
				select {
				case addr = <-address:
				case <-time.After(5 * time.Second):
					t.Fatal("HTTP server did not start")
				}
				responseDone := make(chan error, 1)
				go func() {
					response, err := http.Get("http://" + addr)
					if err == nil {
						response.Body.Close()
					}
					responseDone <- err
				}()
				select {
				case <-requestStarted:
				case <-time.After(5 * time.Second):
					t.Fatal("HTTP request did not start")
				}
				cancel()
				select {
				case <-controllerCancelled:
				case <-time.After(5 * time.Second):
					t.Fatal("controller was not cancelled alongside HTTP shutdown")
				}
				close(requestRelease)
				select {
				case err := <-responseDone:
					require.NoError(t, err, "in-flight HTTP request must finish gracefully")
				case <-time.After(5 * time.Second):
					t.Fatal("HTTP request did not finish")
				}
			}
			select {
			case <-controllerCancelled:
			case <-time.After(5 * time.Second):
				t.Fatal("controller was not cancelled")
			}
			select {
			case err := <-finished:
				t.Fatalf("serve returned before controller joined: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			close(controllerRelease)
			select {
			case err := <-finished:
				if failHTTP {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("serve did not join controller")
			}
		})
	}
}
