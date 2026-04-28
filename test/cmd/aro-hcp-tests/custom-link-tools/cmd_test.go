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

package customlinktools

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"

	"k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-Tools/testutil"
	"github.com/Azure/ARO-HCP/test/util/timing"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

func decodeQueryFromLinkURL(t *testing.T, linkURL string) string {
	t.Helper()

	parsedURL, err := url.Parse(linkURL)
	if err != nil {
		t.Fatalf("failed to parse link URL: %v", err)
	}

	encodedQuery := parsedURL.Query().Get("query")
	if encodedQuery == "" {
		t.Fatalf("missing query parameter in URL: %s", linkURL)
	}

	compressedQuery, err := base64.StdEncoding.DecodeString(encodedQuery)
	if err != nil {
		t.Fatalf("failed to base64 decode query parameter: %v", err)
	}

	gzipReader, err := gzip.NewReader(bytes.NewReader(compressedQuery))
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer gzipReader.Close()

	decodedQuery, err := io.ReadAll(gzipReader)
	if err != nil {
		t.Fatalf("failed to read decompressed query: %v", err)
	}

	return string(decodedQuery)
}

func assertAllServiceLinkQueriesContainTimeWindow(t *testing.T, links []LinkDetails, expectedStart, expectedEnd string) {
	t.Helper()

	if len(links) != 8 {
		t.Fatalf("expected 8 service links, got %d", len(links))
	}

	startDateTime := "datetime(" + expectedStart + ")"
	endDateTime := "datetime(" + expectedEnd + ")"

	for _, link := range links {
		decodedQuery := decodeQueryFromLinkURL(t, link.URL)
		if !strings.Contains(decodedQuery, startDateTime) {
			t.Fatalf("link %q does not contain expected start time %q\nquery: %s", link.DisplayName, expectedStart, decodedQuery)
		}
		if !strings.Contains(decodedQuery, endDateTime) {
			t.Fatalf("link %q does not contain expected end time %q\nquery: %s", link.DisplayName, expectedEnd, decodedQuery)
		}
	}
}

func TestGeneratedHTML(t *testing.T) {
	ctx := logr.NewContext(t.Context(), testr.New(t))
	tmpdir := t.TempDir()

	kusto := KustoInfo{
		KustoName:                      "hcp-dev-us-2",
		KustoRegion:                    "eastus2",
		ServiceLogsDatabase:            "ServiceLogs",
		HostedControlPlaneLogsDatabase: "HostedControlPlaneLogs",
	}

	opts := Options{
		completedOptions: &completedOptions{
			TimingInputDir:  "../testdata/output",
			OutputDir:       tmpdir,
			SvcClusterName:  "hcp-underlay-prow-usw3j688-svc-1",
			MgmtClusterName: "hcp-underlay-prow-usw3j688-mgmt-1",
			SubscriptionID:  "00000000-0000-0000-0000-000000000000",
			Kusto:           kusto,
			Clock:           clocktesting.NewFakePassiveClock(time.Date(2022, 3, 17, 19, 0, 0, 0, time.UTC)),
			Steps: []pipeline.NodeInfo{
				{Info: pipeline.ExecutionInfo{
					StartedAt:  "2022-03-17T17:30:00Z",
					FinishedAt: "2022-03-17T18:30:00Z",
				}},
			},
		},
	}
	err := opts.Run(ctx)
	if err != nil {
		t.Fatalf("failed to run custom link tools: %v", err)
	}

	testutil.CompareFileWithFixture(t, filepath.Join(tmpdir, "custom-link-tools.html"), testutil.WithSuffix("custom-link-tools"))
	testutil.CompareFileWithFixture(t, filepath.Join(tmpdir, "custom-link-tools-test-table.html"), testutil.WithSuffix("custom-link-tools-test-table"))
	testutil.CompareFileWithFixture(t, filepath.Join(tmpdir, "custom-link-tools-commands.html"), testutil.WithSuffix("custom-link-tools-commands"))
}

func TestGeneratedHTMLWithoutStepsUsesTimingFallback(t *testing.T) {
	ctx := logr.NewContext(t.Context(), testr.New(t))
	tmpdir := t.TempDir()

	kusto := KustoInfo{
		KustoName:                      "hcp-dev-us-2",
		KustoRegion:                    "eastus2",
		ServiceLogsDatabase:            "ServiceLogs",
		HostedControlPlaneLogsDatabase: "HostedControlPlaneLogs",
	}

	opts := Options{
		completedOptions: &completedOptions{
			TimingInputDir:  "../testdata/output",
			OutputDir:       tmpdir,
			Steps:           nil,
			SvcClusterName:  "hcp-underlay-prow-usw3j688-svc-1",
			MgmtClusterName: "hcp-underlay-prow-usw3j688-mgmt-1",
			Kusto:           kusto,
			Clock:           clocktesting.NewFakePassiveClock(time.Date(2022, 3, 17, 19, 0, 0, 0, time.UTC)),
		},
	}
	err := opts.Run(ctx)
	if err != nil {
		t.Fatalf("failed to run custom link tools: %v", err)
	}

	testutil.CompareFileWithFixture(t, filepath.Join(tmpdir, "custom-link-tools.html"), testutil.WithSuffix("custom-link-tools-no-steps"))
	testutil.CompareFileWithFixture(t, filepath.Join(tmpdir, "custom-link-tools-test-table.html"), testutil.WithSuffix("custom-link-tools-test-table"))
	testutil.CompareFileWithFixture(t, filepath.Join(tmpdir, "custom-link-tools-commands.html"), testutil.WithSuffix("custom-link-tools-commands-no-steps"))
}

func TestGetServiceLogLinksWithExplicitTimeWindow(t *testing.T) {
	kusto := KustoInfo{
		KustoName:                      "hcp-dev-us-2",
		KustoRegion:                    "eastus2",
		ServiceLogsDatabase:            "ServiceLogs",
		HostedControlPlaneLogsDatabase: "HostedControlPlaneLogs",
	}

	tw := timing.TimeWindow{
		Start: time.Date(2022, 3, 17, 16, 0, 0, 0, time.UTC),
		End:   time.Date(2022, 3, 17, 19, 30, 0, 0, time.UTC),
	}

	links, err := getServiceLogLinks(tw, "svc-cluster", "mgmt-cluster", kusto)
	if err != nil {
		t.Fatalf("failed to get service log links: %v", err)
	}

	assertAllServiceLinkQueriesContainTimeWindow(t, links, "2022-03-17T16:00:00.0000000Z", "2022-03-17T19:30:00.0000000Z")
}

func TestComputeTimeWindowErrorsWithNoTimingData(t *testing.T) {
	ctx := logr.NewContext(t.Context(), testr.New(t))
	_, err := timing.ComputeTimeWindow(ctx, clock.RealClock{}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error when no timing data is available")
	}
}

func TestGetServiceLogLinksUsesCLIStartFallbackWhenStepsAndTimingUnavailable(t *testing.T) {
	ctx := logr.NewContext(t.Context(), testr.New(t))

	startTimeFallback, err := time.Parse(time.RFC3339, "2022-03-17T17:00:00Z")
	if err != nil {
		t.Fatalf("failed to parse fallback start time: %v", err)
	}

	kusto := KustoInfo{
		KustoName:                      "hcp-dev-us-2",
		KustoRegion:                    "eastus2",
		ServiceLogsDatabase:            "ServiceLogs",
		HostedControlPlaneLogsDatabase: "HostedControlPlaneLogs",
	}

	fakeClock := clocktesting.NewFakePassiveClock(time.Date(2022, 3, 17, 19, 0, 0, 0, time.UTC))
	tw, err := timing.ComputeTimeWindow(ctx, fakeClock, nil, nil, &startTimeFallback)
	if err != nil {
		t.Fatalf("failed to compute time window: %v", err)
	}

	links, err := getServiceLogLinks(tw, "svc-cluster", "mgmt-cluster", kusto)
	if err != nil {
		t.Fatalf("failed to get service log links: %v", err)
	}

	assertAllServiceLinkQueriesContainTimeWindow(t, links, "2022-03-17T17:00:00.0000000Z", "2022-03-17T19:45:00.0000000Z")
}

func TestCompleteFailsWithInvalidStartTimeFallback(t *testing.T) {
	tmpDir := t.TempDir()
	renderedConfigPath := filepath.Join(tmpDir, "rendered-config.yaml")

	err := os.WriteFile(renderedConfigPath, []byte(`
svc:
  aks:
    name: svc-cluster
mgmt:
  aks:
    name: mgmt-cluster
kusto:
  kustoName: hcp-dev-us-2
  serviceLogsDatabase: ServiceLogs
  hostedControlPlaneLogsDatabase: HostedControlPlaneLogs
`), 0644)
	if err != nil {
		t.Fatalf("failed to write rendered config fixture: %v", err)
	}

	validated := &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions: &RawOptions{
				TimingInputDir:    tmpDir,
				OutputDir:         tmpDir,
				RenderedConfig:    renderedConfigPath,
				StartTimeFallback: "not-a-time",
			},
		},
	}

	_, err = validated.Complete(logr.NewContext(t.Context(), testr.New(t)))
	if err == nil {
		t.Fatal("expected Complete to fail for invalid --start-time-fallback")
	}

	if !strings.Contains(err.Error(), "failed to parse --start-time-fallback") {
		t.Fatalf("expected parse error for --start-time-fallback, got: %v", err)
	}
}
