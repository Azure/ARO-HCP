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

package gatherobservability

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/test/util/timing"
)

// collectFrontendSamples preserves stored samples, not evaluations of rate or
// aggregation expressions. Range selectors retain replica and histogram-bucket
// labels and the original sample timestamps in the native HTTP response.
func collectFrontendSamples(ctx context.Context, base policy.Transporter, cred azcore.TokenCredential, endpoint string, window timing.TimeWindow, evidence *evidenceCollector) {
	if evidence == nil {
		return
	}
	collectionStart := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	profile := evidenceRequest{
		Kind: "stored-samples", Source: "prometheus", Workspace: "svc",
		Title: "frontend stored-sample profile", Start: window.Start, End: window.End,
	}
	evidence.note(profile, "complete", "Original requested run window; profile adds 30m of history. Query bounds are (start,end], with 1ms lower-bound padding at the earliest requested boundary to include that boundary's stored sample. Capture status is recorded separately for each HTTP response.")
	if window.Start.IsZero() || window.End.IsZero() || window.End.Before(window.Start) {
		evidence.note(profile, "failed", "Invalid requested run window; no stored-sample queries issued.")
		return
	}
	if endpoint == "" {
		evidence.note(profile, "skipped", "The svc Prometheus workspace endpoint is unavailable; no stored-sample queries issued.")
		return
	}

	start := window.Start.Add(-30 * time.Minute)
	end := window.End
	partial := false
	if end.After(collectionStart) {
		omitted := profile
		omitted.Start, omitted.End = collectionStart, end
		evidence.note(omitted, "skipped", "Future region omitted: stored samples cannot be collected after collection start, including future end-grace.")
		end = collectionStart
		partial = true
	}
	if start.After(end) {
		evidence.note(profile, "skipped", "The entire requested profile interval is in the future; no stored-sample queries issued.")
		return
	}

	// Plan unpadded intervals so an exact 15m multiple does not create a
	// separate 1ms query. Only the oldest uncapped selector receives padding;
	// adjacent chunks otherwise share one boundary without overlap.
	earliest := end.Add(-6 * time.Hour)
	capped := !start.After(earliest)
	if start.Before(earliest) {
		omitted := profile
		omitted.Start, omitted.End = start, earliest
		evidence.note(omitted, "limited", "Earlier history omitted by the 6h profile limit; the retained interval has an exclusive lower bound without padding.")
		start = earliest
		partial = true
	}
	profile.Start, profile.End = start, end

	selectors := [...]string{
		"frontend_http_requests_duration_seconds_bucket",
		"frontend_http_requests_duration_seconds_count",
		"frontend_http_requests_duration_seconds_sum",
		"frontend_http_requests_total",
		"sli:frontend_http:latency_p99:rate5m",
		"sli:frontend_http:latency_p95:rate5m",
		`up{service="aro-hcp-frontend-metrics"}`,
		`scrape_duration_seconds{service="aro-hcp-frontend-metrics"}`,
		`scrape_samples_scraped{service="aro-hcp-frontend-metrics"}`,
	}
	completed, failed, empty := 0, 0, 0
	// Visit newest chunks first, all metric families per chunk. A deadline or
	// budget limit therefore favors recent evidence without spending the entire
	// allowance on the historical samples of a single metric family.
	for chunkEnd := end; chunkEnd.After(start); {
		chunkStart := chunkEnd.Add(-15 * time.Minute)
		if chunkStart.Before(start) {
			chunkStart = start
		}
		// Prometheus range durations use milliseconds. Round up a fractional
		// duration and pad only the oldest uncapped selector by 1ms to include
		// the requested lower boundary. Record the actual exclusive bound,
		// including this rounding/padding, on every native response entry.
		duration := chunkEnd.Sub(chunkStart)
		if !capped && chunkStart.Equal(start) {
			duration += time.Millisecond
		}
		milliseconds := duration / time.Millisecond
		if duration%time.Millisecond != 0 {
			milliseconds++
		}
		queryStart := chunkEnd.Add(-milliseconds * time.Millisecond)
		for _, selector := range selectors {
			if err := ctx.Err(); err != nil {
				evidence.note(profile, "limited", fmt.Sprintf("Partial profile: context canceled or deadline reached after %d successful, %d failed, and %d empty queries; remaining coverage was not requested.", completed, failed, empty))
				return
			}
			if evidence.remainingBytes() <= 0 {
				evidence.note(profile, "limited", fmt.Sprintf("Partial profile: shared evidence byte budget exhausted after %d successful, %d failed, and %d empty queries; remaining coverage was not requested.", completed, failed, empty))
				return
			}
			metadata := profile
			metadata.Title, metadata.Start, metadata.End = selector, queryStart, chunkEnd
			query := fmt.Sprintf("%s[%dms]", selector, milliseconds)
			response, err := queryInstant(ctx, evidence.client(base, metadata), cred, endpoint, query, chunkEnd)
			if ctx.Err() != nil {
				evidence.note(profile, "limited", fmt.Sprintf("Partial profile: context canceled or deadline reached during a query after %d successful, %d failed, and %d empty queries; remaining coverage was not requested.", completed, failed, empty))
				return
			}
			if err != nil {
				failed++
				partial = true
				// Native HTTP error payloads are already preserved by the transport;
				// do not duplicate possibly large or sensitive error text in notes.
				evidence.note(metadata, "failed", "Stored-sample query failed; inspect the native HTTP entry when available. Authentication or request setup failures may not have an HTTP response.")
				continue
			}
			completed++
			if len(response.Data.Result) == 0 {
				empty++
				evidence.note(metadata, "complete", "Query succeeded but returned no stored series. History may be unavailable or the series may be absent; this is not a query failure and retention cannot be inferred.")
			}
		}
		chunkEnd = chunkStart
	}
	status := "complete"
	if partial || evidence.remainingBytes() <= 0 || ctx.Err() != nil {
		status = "limited"
	}
	evidence.note(profile, status, fmt.Sprintf("Profile requests finished: %d successful, %d failed, %d empty. Native HTTP entries separately report capture completeness; successful requests do not establish historical sample availability.", completed, failed, empty))
}
