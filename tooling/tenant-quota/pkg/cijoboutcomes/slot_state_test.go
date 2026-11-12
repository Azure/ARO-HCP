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

package cijoboutcomes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

const validSlotState = `version: 2
deploy_environment: ci00
runtime_region: westus3
leased_resource_name: slot-01
leases:
  primary:
    resource_type: slot-pool
    resource_name: slot-01
  assets:
    infrastructure_identities:
      - {resource_type: identity-pool, resource_name: asset-02}
      - {resource_type: identity-pool, resource_name: asset-01}
    future_asset_kind:
      - {resource_type: future-pool, resource_name: asset-03}
slot:
  region: catalog-region
  resource_type: slot-pool
  resource_name: slot-01
  requirements:
    - {kind: not-a-lease}
  assets:
    e2e_identities:
      resource_groups: [not-a-boskos-lease]
`

func TestParseSlotState(t *testing.T) {
	got, err := parseSlotState([]byte(validSlotState))
	require.NoError(t, err)
	require.EqualValues(t, 2, *got.Version)
	require.Equal(t, slotStateAvailable, got.Status)
	require.Equal(t, "westus3", got.RuntimeRegion)
	require.Equal(t, "slot-01", got.LeasedSlot)
	require.Equal(t, []leasedAsset{
		{Kind: "future_asset_kind", ResourceName: "asset-03"},
		{Kind: "infrastructure_identities", ResourceName: "asset-01"},
		{Kind: "infrastructure_identities", ResourceName: "asset-02"},
	}, got.LeasedAssets)
	for range 10 {
		again, err := parseSlotState([]byte(validSlotState))
		require.NoError(t, err)
		require.Equal(t, got, again, "map iteration must not change the payload order")
	}
}

func TestParseSlotStateWithoutAdditionalLeases(t *testing.T) {
	start := strings.Index(validSlotState, "  assets:")
	end := strings.Index(validSlotState, "\nslot:")
	for _, assets := range []string{"", "  assets: {}\n", "  assets: null\n", "  assets: {infrastructure_identities: []}\n"} {
		t.Run(assets, func(t *testing.T) {
			got, err := parseSlotState([]byte(validSlotState[:start] + assets + validSlotState[end:]))
			require.NoError(t, err)
			require.NotNil(t, got.LeasedAssets)
			data, err := json.Marshal(got.LeasedAssets)
			require.NoError(t, err)
			require.Equal(t, "[]", string(data))
		})
	}
}

func TestParseSlotStateRejectsInvalidEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, old, replacement string
	}{
		{"missing version", "version: 2", ""},
		{"null version", "version: 2", "version: null"},
		{"zero version", "version: 2", "version: 0"},
		{"negative version", "version: 2", "version: -1"},
		{"version overflows Kusto int", "version: 2", "version: 2147483648"},
		{"invalid version", "version: 2", "version: [2]"},
		{"duplicate version", "version: 2", "version: 2\nversion: 2"},
		{"missing region", "runtime_region: westus3", ""},
		{"blank region", "runtime_region: westus3", "runtime_region: '  '"},
		{"null region", "runtime_region: westus3", "runtime_region: null"},
		{"missing primary", "primary:", "ignored_primary:"},
		{"missing primary name", "resource_name: slot-01", "resource_name: ''"},
		{"missing primary type", "resource_type: slot-pool", "resource_type: ''"},
		{"wrong slot", "leased_resource_name: slot-01", "leased_resource_name: slot-02"},
		{"missing resolved slot", "slot:\n", "ignored_slot:\n"},
		{"returned primary", "primary:", "primary:\n    return_state: returned"},
		{"empty kind", "future_asset_kind:", "'  ':"},
		{"incomplete asset", "resource_type: future-pool", "resource_type: ''"},
		{"null asset", "{resource_type: future-pool, resource_name: asset-03}", "null"},
		{"duplicate asset", "resource_name: asset-02", "resource_name: asset-01"},
		{"primary also an asset", "resource_name: asset-02", "resource_name: slot-01"},
		{"returning asset", "resource_type: future-pool", "return_state: returning, resource_type: future-pool"},
		{"wrong assets shape", "assets:\n    infrastructure_identities:", "assets: []\n  ignored:\n    infrastructure_identities:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.ReplaceAll(validSlotState, tc.old, tc.replacement)
			got, err := parseSlotState([]byte(body))
			require.Error(t, err)
			require.NotErrorIs(t, err, errSlotStateVersion)
			require.Empty(t, got.RuntimeRegion)
			require.Empty(t, got.LeasedSlot)
			require.Nil(t, got.LeasedAssets, "invalid snapshots must not leak partial enrichment")
		})
	}
	for _, body := range []string{"", "null", "{}", "[]", "[:", validSlotState + "\n---\nversion: 2\n", validSlotState + "\n---\n"} {
		_, err := parseSlotState([]byte(body))
		require.Error(t, err, "body %q", body)
	}
}

func TestParseSlotStateUnsupportedVersion(t *testing.T) {
	for _, version := range []string{"1", "3"} {
		got, err := parseSlotState([]byte("version: " + version + "\nleases: future-format\n"))
		require.ErrorIs(t, err, errSlotStateVersion)
		require.NotNil(t, got.Version)
		require.Nil(t, got.LeasedAssets)
		require.Empty(t, got.RuntimeRegion)
	}
}

func TestFetchSlotState(t *testing.T) {
	const listingPath = artifactTestPrefix + "/artifacts/"
	const statePath = artifactTestRoot + slotStateArtifactPath
	info, err := snapshot.ParseProwURL(artifactTestURL)
	require.NoError(t, err)
	for _, tc := range []struct {
		name, target, body, wantStatus string
		status                         int
		wantErr                        bool
	}{
		{name: "available", wantStatus: slotStateAvailable},
		{name: "missing directory", target: listingPath, body: `{}`, wantStatus: slotStateAbsent},
		{name: "listing 404", target: listingPath, status: 404, wantStatus: slotStateAbsent},
		{name: "listing 503", target: listingPath, status: 503, wantErr: true},
		{name: "missing file", target: statePath, status: 404, wantStatus: slotStateAbsent},
		{name: "empty file", target: statePath, wantStatus: slotStateMalformed},
		{name: "malformed", target: statePath, body: `[:`, wantStatus: slotStateMalformed},
		{name: "invalid evidence", target: statePath, body: "version: 2\n", wantStatus: slotStateMalformed},
		{name: "unsupported", target: statePath, body: "version: 3\n", wantStatus: slotStateUnsupported},
		{name: "forbidden", target: statePath, status: 403, wantErr: true},
		{name: "throttled", target: statePath, status: 429, wantErr: true},
		{name: "server failure", target: statePath, status: 503, wantErr: true},
		{name: "network failure", target: statePath, status: -1, wantErr: true},
		{name: "interrupted body", target: statePath, status: -2, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objects := map[string]string{
				listingPath: `{"prefixes":["` + artifactTestRoot + `"]}`,
				statePath:   validSlotState,
			}
			if tc.target != "" {
				objects[tc.target] = tc.body
			}
			var problems []string
			ctx := snapshot.WithProwArtifactProblemHandler(t.Context(), func(source, reason string) {
				problems = append(problems, source+"/"+reason)
			})
			got, err := fetchSlotState(ctx, artifactClient(t, objects, map[string]int{tc.target: tc.status}), info)
			require.Equal(t, tc.wantErr, err != nil, "%v", err)
			require.Equal(t, tc.wantStatus, got.Status)
			if tc.wantErr || tc.wantStatus == slotStateAvailable {
				require.Empty(t, problems)
			} else {
				require.Equal(t, []string{"slot-state/" + tc.wantStatus}, problems)
				require.Empty(t, got.RuntimeRegion)
				require.Empty(t, got.LeasedSlot)
				require.Nil(t, got.LeasedAssets)
			}
			if tc.wantStatus == slotStateAbsent {
				require.Nil(t, got.Version)
			}
			if tc.wantStatus == slotStateUnsupported {
				require.EqualValues(t, 3, *got.Version)
			}
		})
	}
}

func TestFetchSlotStateCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client := &http.Client{Transport: artifactTransport(func(r *http.Request) (*http.Response, error) {
		return nil, r.Context().Err()
	})}
	info, err := snapshot.ParseProwURL(artifactTestURL)
	require.NoError(t, err)
	_, err = fetchSlotState(ctx, client, info)
	require.ErrorIs(t, err, context.Canceled)
}

func TestSlotStateEnrichmentForAllJobFamilies(t *testing.T) {
	for _, prefix := range []string{
		artifactTestPrefix,
		"logs/branch-ci-Azure-ARO-HCP-main-e2e-parallel/123",
		"pr-logs/pull/Azure_ARO-HCP/1/pull-ci-Azure-ARO-HCP-main-e2e-parallel/123",
		"pr-logs/pull/batch/pull-ci-Azure-ARO-HCP-main-e2e-parallel/123",
	} {
		t.Run(prefix, func(t *testing.T) {
			root := prefix + "/artifacts/e2e-parallel/"
			objects := map[string]string{
				prefix + "/artifacts/":                                       `{"prefixes":["` + root + `"]}`,
				prefix + "/prowjob.json":                                     `{"status":{"startTime":"2026-01-01T00:00:00Z"}}`,
				root + slotStateArtifactPath:                                 validSlotState,
				root + "aro-hcp-provision-environment/artifacts/config.yaml": "kusto: {location: not-runtime-region}\nsvc: {aks: {name: svc}}\nmgmt: {aks: {name: mgmt}}\n",
			}
			prowURL := "https://prow.ci.openshift.org/view/gs/test-platform-results/" + prefix
			detail, err := fetchJobOutcomeDetail(t.Context(), artifactClient(t, objects, nil), prowURL)
			require.NoError(t, err)
			info, err := snapshot.ParseProwURL(prowURL)
			require.NoError(t, err)
			outcome := outcomeForProw(info, prowCompletion{Result: "SUCCESS"}, detail)
			require.Equal(t, "westus3", outcome.RuntimeRegion)
			require.Equal(t, "slot-01", outcome.LeasedSlot)
			require.Len(t, outcome.LeasedAssets, 3)
			if info.IsPullRequest() {
				require.Equal(t, "svc", outcome.SvcCluster)
				require.Equal(t, "mgmt", outcome.MgmtCluster)
			}
		})
	}
}

func TestSlotStateOutcomeJSON(t *testing.T) {
	metadata, err := parseSlotState([]byte(validSlotState))
	require.NoError(t, err)
	for _, tc := range []struct {
		name     string
		metadata slotStateMetadata
		assets   string
	}{
		{"available", metadata, `[{"kind":"future_asset_kind","resourceName":"asset-03"},{"kind":"infrastructure_identities","resourceName":"asset-01"},{"kind":"infrastructure_identities","resourceName":"asset-02"}]`},
		{"no extra leases", slotStateMetadata{LeasedAssets: []leasedAsset{}}, `[]`},
		{"absent", slotStateMetadata{}, `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := outcomeForProw(&snapshot.ProwJobInfo{ProwID: "123"}, prowCompletion{}, runDetail{SlotState: tc.metadata})
			payload, err := encodeRows([]ciJobOutcome{row})
			require.NoError(t, err)
			var got map[string]json.RawMessage
			require.NoError(t, json.NewDecoder(payload).Decode(&got))
			require.JSONEq(t, tc.assets, string(got["leasedAssets"]))
			_, hasVersion := got["slotStateVersion"]
			require.False(t, hasVersion)
			_, hasStatus := got["slotStateStatus"]
			require.False(t, hasStatus)
		})
	}
}

func TestReconcileSlotStateRetryAndVisibility(t *testing.T) {
	w := newTestWriter(t)
	prefix := strings.Replace(artifactTestPrefix, "/123", "/"+string(testBuildID), 1)
	root := prefix + "/artifacts/e2e-parallel/"
	uri := JobURI("gs://test-platform-results/" + prefix)
	statePath := root + slotStateArtifactPath
	objects := map[string]string{
		prefix + "/artifacts/":   `{"prefixes":["` + root + `"]}`,
		prefix + "/prowjob.json": `{"status":{"startTime":"2026-01-01T00:00:00Z"}}`,
		statePath:                validSlotState,
	}
	statuses := map[string]int{statePath: 503}
	w.client = artifactClient(t, objects, statuses)
	w.jobDetail = fetchJobOutcomeDetail
	runVisible := false
	w.tagExists = func(_ context.Context, kind batchKind, _ BuildID) (bool, error) {
		return kind != runBatch || runVisible, nil
	}
	submissions, acknowledgments := 0, 0
	w.submitProcessed = func(context.Context, BuildID) error { acknowledgments++; return nil }
	w.submit = func(_ context.Context, kind batchKind, id BuildID, payload *bytes.Buffer) error {
		require.Equal(t, runBatch, kind)
		require.Equal(t, testBuildID, id)
		var row ciJobOutcome
		require.NoError(t, json.NewDecoder(payload).Decode(&row))
		require.Equal(t, "westus3", row.RuntimeRegion)
		submissions++
		return nil
	}
	ctx := testContext(t, w)
	_, err := w.reconcile(ctx, uri)
	require.ErrorContains(t, err, "failed to read slot state")
	require.Zero(t, submissions)
	require.Zero(t, acknowledgments)
	statuses[statePath] = 200
	delay, err := w.reconcile(ctx, uri)
	require.NoError(t, err)
	require.Equal(t, 15*time.Minute, delay)
	require.Equal(t, 1, submissions)
	require.Zero(t, acknowledgments)
	w.jobDetail = func(context.Context, *http.Client, string) (runDetail, error) {
		t.Fatal("accepted or visible run batch must not redownload enrichment")
		return runDetail{}, nil
	}
	_, err = w.reconcile(ctx, uri)
	require.NoError(t, err)
	require.Equal(t, 1, submissions)
	require.Zero(t, acknowledgments, "cached acceptance is not persistence")
	runVisible = true
	_, err = w.reconcile(ctx, uri)
	require.NoError(t, err)
	require.Equal(t, 1, acknowledgments)
}

func TestSlotStateDoesNotBackfillExistingRuns(t *testing.T) {
	for _, processed := range []bool{false, true} {
		t.Run(fmt.Sprintf("processed=%v", processed), func(t *testing.T) {
			w := newTestWriter(t)
			w.processedExists = func(context.Context, BuildID) (bool, error) { return processed, nil }
			w.tagExists = func(context.Context, batchKind, BuildID) (bool, error) { return true, nil }
			w.jobDetail = func(context.Context, *http.Client, string) (runDetail, error) {
				t.Fatal("existing run must not fetch slot state")
				return runDetail{}, nil
			}
			w.submit = func(context.Context, batchKind, BuildID, *bytes.Buffer) error {
				t.Fatal("existing run must not be resubmitted")
				return nil
			}
			_, err := w.reconcile(testContext(t, w), testJobURI)
			require.NoError(t, err)
		})
	}
}
