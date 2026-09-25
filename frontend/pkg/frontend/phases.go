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
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Azure/ARO-HCP/internal/utils"
)

type phaseName string

// Phase names describe logical work across routes. Dotted children are included
// in their parent's duration; neither durations nor their quantiles are additive.
const (
	PhaseBodyRead                   phaseName = "body_read"
	PhaseAPIVersionValidation       phaseName = "api_version_validation"
	PhaseSubscriptionValidation     phaseName = "subscription_validation"
	PhaseSubscriptionValidationRead phaseName = "subscription_validation.read"
	PhaseAuditSend                  phaseName = "audit.send"
	PhaseResourceRead               phaseName = "resource_read"
	// ResourceList includes conversion interleaved with fetching list pages.
	PhaseResourceList      phaseName = "resource_list"
	PhaseDecode            phaseName = "decode"
	PhaseAdmission         phaseName = "admission"
	PhaseAdmissionConflict phaseName = "admission.conflict"
	PhaseAdmissionPrefetch phaseName = "admission.prefetch"
	PhaseAdmissionMutate   phaseName = "admission.mutate"
	PhaseAdmissionValidate phaseName = "admission.validate"
	PhasePersistPrepare    phaseName = "persist.prepare"
	PhasePersistExecute    phaseName = "persist.execute"
	PhaseResponseEncode    phaseName = "response.encode"
	// ResponseWrite includes encoding when the response helper performs it.
	PhaseResponseWrite phaseName = "response.write"
)

const phaseDurationName = "frontend_http_request_phase_duration_seconds"
const notRoutedPhaseLabel = "<not routed>"

type phaseMetricsKey struct{}

type phaseMetrics struct {
	duration *prometheus.HistogramVec
	method   string
}

type phaseTimer struct {
	ctx   context.Context
	phase phaseName
	start time.Time
}

func startPhase(ctx context.Context, phase phaseName) phaseTimer {
	return phaseTimer{ctx: ctx, phase: phase, start: time.Now()}
}

// End records a completed interval, including failed calls. Call it before
// returning an error, and before invoking downstream middleware. A defer is
// appropriate only when the phase really covers the remainder of the function.
func (t phaseTimer) End() {
	duration := time.Since(t.start).Seconds()
	metrics, _ := t.ctx.Value(phaseMetricsKey{}).(*phaseMetrics)
	if metrics == nil {
		return // Helpers can also be called outside the ARM request pipeline.
	}
	route := notRoutedPhaseLabel
	if pattern := PatternFromContext(t.ctx); pattern != nil && *pattern != "" {
		route = muxPatternRoute(*pattern)
	}
	metrics.duration.WithLabelValues(route, metrics.method, string(t.phase)).Observe(duration)
	utils.LoggerFromContext(t.ctx).Info("request phase complete",
		"phase", string(t.phase), "duration_seconds", duration, "route", route)
}
