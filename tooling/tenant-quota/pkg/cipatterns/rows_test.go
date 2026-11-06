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

package cipatterns

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/tooling/cifailurepatterns"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/cifailures"
)

const alertName = "[aro-hcp-observability] [hcp] alert KubePodNotReady does not fire"
const multiSubjectText = "Firing 1:\n  State: Fired (not resolved)\n  Labels: pod=\"etcd-0\"\n  Description: Pod is not ready.\nFiring 2:\n  State: Fired (not resolved)\n  Labels: pod=\"router-767bb4bbcd-xqtnw\"\n  Description: Pod is not ready.\n"

func sampleInput() input {
	return input{observation: cifailures.Observation{Origin: "https://example.test/ServiceLogs", BuildID: "build-1", Source: "e2e", TestID: "test-1", Text: "context deadline exceeded"}, multiplicity: 1, nameStatus: "missing"}
}

func computedRow(i input) resultRow {
	r := envelope(i, compiledFailurePatternVersion())
	r.Findings = cifailurepatterns.Extract(cifailurepatterns.Input{Text: r.Message, TestName: r.TestName, Source: r.Source})
	r.ComputedAt = time.Date(2026, 11, 4, 1, 2, 3, 0, time.UTC)
	return r
}

func TestIdentityUsesRawFingerprintAndExactSemanticContext(t *testing.T) {
	i := sampleInput()
	i.observation.StartedAt = time.Date(2026, 1, 2, 3, 4, 5, 123400000, time.FixedZone("offset", 3600))
	r := envelope(i, compiledFailurePatternVersion())
	require.Equal(t, cifailures.Fingerprint(i.observation), r.RawFingerprint)
	for _, test := range []struct {
		name             string
		mutate           func(*input)
		sameRaw, sameKey bool
	}{
		{"repeat", func(i *input) { i.multiplicity++ }, true, true},
		{"environment", func(i *input) { i.observation.Environment = "another" }, true, true},
		{"name metadata", func(i *input) { i.observation.NameVariants = 4; i.nameStatus = "ambiguous" }, true, true},
		{"name arrives", func(i *input) { i.observation.TestName = "name 🧪" }, true, false},
		{"exact bytes", func(i *input) { i.observation.Text += "\r\n" }, false, false},
		{"timestamp", func(i *input) { i.observation.CleanupStartTime = time.Now() }, false, false},
		{"source", func(i *input) { i.observation.Source = "observability" }, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := i
			test.mutate(&changed)
			got := envelope(changed, compiledFailurePatternVersion())
			require.Equal(t, test.sameRaw, r.RawFingerprint == got.RawFingerprint)
			require.Equal(t, test.sameKey, r.ResultKey == got.ResultKey)
		})
	}
	require.NotEqual(t, resultKey("ab", "c", compiledFailurePatternVersion()), resultKey("a", "bc", compiledFailurePatternVersion()))
	require.NotEqual(t, r.ResultKey, resultKey(r.RawFingerprint, r.TestName, failurePatternVersion{extractorVersion: "fp-other", resultSchemaVersion: 1}))
	require.NotEqual(t, r.ResultKey, resultKey(r.RawFingerprint, r.TestName, failurePatternVersion{extractorVersion: cifailurepatterns.Version, resultSchemaVersion: 2}))
}

func TestTimestampNullZeroNormalization(t *testing.T) {
	i := sampleInput()
	r := envelope(i, compiledFailurePatternVersion())
	require.Nil(t, r.StartedAt)
	encoded, err := json.Marshal(r)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"startedAt":null`)
	var roundTrip resultRow
	require.NoError(t, json.Unmarshal(encoded, &roundTrip))
	require.Nil(t, roundTrip.StartedAt)
	localZero := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	i.observation.StartedAt = localZero
	require.Equal(t, r, envelope(i, compiledFailurePatternVersion()))
}

func TestPersistedFindingsAreDirectUncappedExtract(t *testing.T) {
	for _, test := range []struct {
		name, text, testName, source string
		count                        int
	}{
		{"empty", "", "", "e2e", 1},
		{"exact Unicode bytes", "\xef\xbb\xbf🧪\r\n  context deadline exceeded\x00\n", "bad'); union x // 🧪", "e2e", 1},
		{"multiple subjects", multiSubjectText, alertName, "observability", 2},
		{"unknown subject", "Firing 1:\n  Labels: namespace=\"x\"\n", alertName, "observability", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := newTestController(t)
			i := sampleInput()
			i.observation.Text, i.observation.TestName, i.observation.Source = test.text, test.testName, test.source
			var got []resultRow
			c.submit = func(_ context.Context, payload *bytes.Buffer) error { return decodePayload(payload.Bytes(), &got) }
			delay, err := c.reconcileInputs(t.Context(), compiledFailurePatternVersion(), []input{i})
			require.NoError(t, err)
			require.Positive(t, delay)
			require.Len(t, got, 1)
			require.Equal(t, test.text, got[0].Message)
			require.Equal(t, cifailurepatterns.Extract(cifailurepatterns.Input{Text: test.text, TestName: test.testName, Source: test.source}), got[0].Findings)
			require.Len(t, got[0].Findings, test.count)
			_, err = validatedVisibleResults([]input{i}, compiledFailurePatternVersion(), got)
			require.NoError(t, err)
		})
	}
}

func TestValidatedVisibleResultsDuplicatesAndCorruption(t *testing.T) {
	i := sampleInput()
	good := computedRow(i)
	for _, test := range []struct {
		name   string
		mutate func(*resultRow)
		valid  bool
	}{
		{"identical", func(*resultRow) {}, true},
		{"diagnostic differences", func(r *resultRow) { r.ComputedAt = r.ComputedAt.Add(time.Second); r.ProducerCommit = "other" }, true},
		{"conflict", func(r *resultRow) { r.Findings[0].Canonical += " conflict" }, false},
		{"wrong fingerprint", func(r *resultRow) { r.RawFingerprint = "wrong" }, false},
		{"wrong raw", func(r *resultRow) { r.Message += "\n" }, false},
		{"wrong name", func(r *resultRow) { r.TestName = "other" }, false},
		{"wrong schema", func(r *resultRow) { r.ResultSchemaVersion++ }, false},
		{"no findings", func(r *resultRow) { r.Findings = nil }, false},
		{"no timestamp", func(r *resultRow) { r.ComputedAt = time.Time{} }, false},
		{"bad offset", func(r *resultRow) {
			r.Findings[0].Locations = []cifailurepatterns.Location{{Start: 0, End: len(r.Message) + 1}}
		}, false},
		{"bad outcome", func(r *resultRow) { r.Findings[0].Outcome = "made-up" }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(good)
			require.NoError(t, err)
			var changed resultRow
			require.NoError(t, json.Unmarshal(encoded, &changed))
			test.mutate(&changed)
			rows, err := validatedVisibleResults([]input{i}, compiledFailurePatternVersion(), []resultRow{good, changed})
			if !test.valid {
				require.Error(t, err)
				require.Nil(t, rows)
				return
			}
			require.NoError(t, err)
			require.Len(t, rows, 1)
		})
	}
	for _, encoded := range []string{`null`, `{}`, `[{}]`, `[{"canonical":"x","key":"k","outcome":"matched","rule_id":"r","unknown":1}]`, `[{"canonical":"x","key":"k","outcome":"matched","rule_id":"r","locations":[{"end":1}]}]`} {
		findings, err := decodeFindings([]byte(encoded))
		if err == nil {
			err = validateFindings(findings, "x")
		}
		require.Error(t, err, "%s", encoded)
	}
}

func TestDeterministicBoundedEncoding(t *testing.T) {
	rows := make([]resultRow, maxBatchRows+1)
	for index := range rows {
		i := sampleInput()
		i.observation.TestID = fmt.Sprint(index)
		rows[index] = computedRow(i)
	}
	first, err := encodeBatches(rows)
	require.NoError(t, err)
	require.Len(t, first, 2)
	for left, right := 0, len(rows)-1; left < right; left, right = left+1, right-1 {
		rows[left], rows[right] = rows[right], rows[left]
	}
	second, err := encodeBatches(rows)
	require.NoError(t, err)
	for index := range first {
		require.Equal(t, first[index].payload.Bytes(), second[index].payload.Bytes())
		require.Equal(t, first[index].keys, second[index].keys)
		require.LessOrEqual(t, first[index].payload.Len(), maxBatchBytes)
	}
	oversized := computedRow(sampleInput())
	oversized.Message = strings.Repeat("\x00", maxRowBytes/6+1)
	batches, err := encodeBatches([]resultRow{oversized})
	require.ErrorContains(t, err, "exceeds")
	require.Nil(t, batches)
	err = validateInputPopulation(make([]input, maxReadRows+1))
	require.Error(t, err)
}
