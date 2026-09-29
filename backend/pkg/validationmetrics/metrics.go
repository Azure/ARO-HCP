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

// Package validationmetrics instruments permission validations without changing their results.
package validationmetrics

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/internal/utils"
)

type Metrics struct {
	duration *prometheus.HistogramVec
	attempts *prometheus.CounterVec
	phases   *prometheus.HistogramVec
	inflight *prometheus.GaugeVec
}

// These names match the code-defined permission validation controllers' queue names.
const (
	ControlPlaneController = "ClusterValidationControlPlaneIdentitiesPermissionsClusterValidation"
	DataPlaneController    = "ClusterValidationDataPlaneIdentitiesPermissionsValidation"
)

var phases = []string{"check_access_wait", "check_access_call", "identity_credentials", "identity_credential_build", "identity_token", "subnet_get", "identity_get", "role_definition_lookup", "persist_result"}

// New registers metrics once with the application's registry, before controllers start.
func New(registerer prometheus.Registerer) *Metrics {
	buckets := []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10, 20, 30, 60, 120, 180, 300, 600}
	factory := promauto.With(registerer)
	metrics := &Metrics{
		duration: factory.NewHistogramVec(prometheus.HistogramOpts{Name: "backend_validation_duration_seconds", Help: "Wall time spent in Validate and its local result validity check, excluding persistence.", Buckets: buckets}, []string{"controller", "outcome"}),
		attempts: factory.NewCounterVec(prometheus.CounterOpts{Name: "backend_validation_attempts_total", Help: "Validation reconciliations by final disposition."}, []string{"controller", "disposition"}),
		phases:   factory.NewHistogramVec(prometheus.HistogramOpts{Name: "backend_validation_phase_duration_seconds", Help: "Wall time spent in validation phases; nested phases may overlap.", Buckets: buckets}, []string{"controller", "phase", "result"}),
		inflight: factory.NewGaugeVec(prometheus.GaugeOpts{Name: "backend_validation_phase_inflight", Help: "Currently executing validation phases."}, []string{"controller", "phase"}),
	}
	// Expose zero baselines before the first call so rate/increase includes its work.
	for _, controller := range []string{ControlPlaneController, DataPlaneController} {
		for _, outcome := range []string{"passed", "failed", "unknown", "skipped", "invalid_result", "panic"} {
			metrics.duration.WithLabelValues(controller, outcome)
		}
		for _, disposition := range []string{"cooldown", "prerequisite_skip", "read_error", "completed", "persist_conflict", "persist_error", "invalid_result", "reported_unknown", "panic"} {
			metrics.attempts.WithLabelValues(controller, disposition)
		}
		for _, phase := range phases {
			metrics.inflight.WithLabelValues(controller, phase)
			for _, result := range []string{"success", "error", "canceled", "deadline_exceeded", "throttled", "conflict"} {
				metrics.phases.WithLabelValues(controller, phase, result)
			}
		}
	}
	return metrics
}

type metricsKey struct{}
type validationKey struct{}

func WithMetrics(ctx context.Context, metrics *Metrics) context.Context {
	return context.WithValue(ctx, metricsKey{}, metrics)
}

// RecordAttempt does nothing when metrics or the scoped controller name are absent.
func RecordAttempt(ctx context.Context, controller, disposition string) {
	metrics, _ := ctx.Value(metricsKey{}).(*Metrics)
	if metrics != nil && controller != "" && disposition != "" {
		metrics.attempts.WithLabelValues(controller, disposition).Inc()
	}
}

// Validation holds one invocation's phase aggregates. Phase calls may run concurrently.
type Validation struct {
	metrics    *Metrics
	controller string
	start      time.Time
	duration   float64
	outcome    string
	mu         sync.Mutex
	active     map[*phaseCall]struct{}
	durations  map[string]float64
	counts     map[string]int
	closed     bool
}

type phaseCall struct {
	name  string
	start time.Time
}

// StartValidation scopes phase instrumentation to an actual Validate invocation.
// The caller must defer Complete, including on panic. A nil Validation is a no-op.
func StartValidation(ctx context.Context, controller string) (context.Context, *Validation) {
	metrics, _ := ctx.Value(metricsKey{}).(*Metrics)
	if metrics == nil || controller == "" {
		return ctx, nil
	}
	v := &Validation{metrics: metrics, controller: controller, start: time.Now(), active: map[*phaseCall]struct{}{}, durations: map[string]float64{}, counts: map[string]int{}}
	return context.WithValue(ctx, validationKey{}, v), v
}

// Observe records elapsed validation time, including the local result validity check,
// before persistence starts. Observe and Complete are called by the owning controller,
// not by phase goroutines.
func (v *Validation) Observe(outcome string) {
	if v == nil {
		return
	}
	v.duration = time.Since(v.start).Seconds()
	v.outcome = outcome
	v.metrics.duration.WithLabelValues(v.controller, outcome).Observe(v.duration)
}

// Complete emits one completion record and cleans up unfinished phases as errors.
// It deliberately does not recover: the controller's existing panic policy still applies.
func (v *Validation) Complete(ctx context.Context, validationName, persistenceResult string) {
	if v == nil {
		return
	}
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return
	}
	v.closed = true
	if v.outcome == "" {
		v.Observe("panic")
	}
	for call := range v.active {
		v.finishLocked(call, "error")
	}
	// Closed validations reject new phases and have no active finish callbacks left
	// to mutate the aggregates. Log the now-immutable maps without holding the lock.
	v.mu.Unlock()
	utils.LoggerFromContext(ctx).Info("Validation completed",
		"validation", validationName, "outcome", v.outcome, "duration_seconds", v.duration,
		"phase_durations_seconds", v.durations, "phase_call_counts", v.counts,
		"persistence_result", persistenceResult)
}

// StartPhase measures wall elapsed time and returns an idempotent completion function.
// Without a validation context it is a no-op. Call finish(err) normally; the owning
// Validation's deferred Complete cleans up unfinished calls on panic as errors.
func StartPhase(ctx context.Context, phase string) func(error) {
	v, _ := ctx.Value(validationKey{}).(*Validation)
	if v == nil {
		return func(error) {}
	}
	if !slices.Contains(phases, phase) {
		return func(error) {}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return func(error) {}
	}
	call := &phaseCall{name: phase, start: time.Now()}
	v.active[call] = struct{}{}
	v.metrics.inflight.WithLabelValues(v.controller, phase).Inc()
	return func(err error) {
		v.mu.Lock()
		defer v.mu.Unlock()
		if _, active := v.active[call]; active {
			v.finishLocked(call, Result(err))
		}
	}
}

func (v *Validation) finishLocked(call *phaseCall, result string) {
	elapsed := time.Since(call.start).Seconds()
	delete(v.active, call)
	v.durations[call.name] += elapsed
	v.counts[call.name]++
	v.metrics.phases.WithLabelValues(v.controller, call.name, result).Observe(elapsed)
	v.metrics.inflight.WithLabelValues(v.controller, call.name).Dec()
}

// Result classifies wrapped errors without inspecting error messages.
func Result(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	}
	var responseError *azcore.ResponseError
	if errors.As(err, &responseError) {
		switch responseError.StatusCode {
		case http.StatusTooManyRequests:
			return "throttled"
		case http.StatusConflict, http.StatusPreconditionFailed:
			return "conflict"
		}
	}
	return "error"
}
