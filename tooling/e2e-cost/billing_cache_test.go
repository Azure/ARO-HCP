// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cost

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// billingCacheClient serves one completed report with two blobs and counts
// report generations; failBlob makes the second blob download fail.
func billingCacheClient(t *testing.T, generations *int, failBlob *bool) *http.Client {
	t.Helper()
	return &http.Client{Transport: billingTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "management.azure.com" {
			*generations++
			return billingResponse(200, billingManifest(billingTestBlob, billingTestBlob+"2")), nil
		}
		if r.URL.String() == billingTestBlob+"2" {
			if *failBlob {
				return billingResponse(404, ""), nil
			}
			return billingResponse(200, billingTestHeader+billingRow("owned", billingTestID, "2026-09-18", "2")), nil
		}
		return billingResponse(200, billingTestHeader+billingRow("owned", billingTestID, "2026-09-17", "1")), nil
	})}
}

func TestBillingCacheSharesReportAcrossCollectors(t *testing.T) {
	cache := t.TempDir()
	generations, failBlob := 0, false
	client := billingCacheClient(t, &generations, &failBlob)
	var collected [][]Charge
	for range 2 {
		s := billingSnapshot()
		CollectWithCache(context.Background(), client, billingCredential{}, s, cache)
		if s.HasErrors() || s.Groups[0].BillingStatus != "complete" || len(s.Groups[0].Resources) != 1 {
			t.Fatalf("cached collection failed: %+v", s)
		}
		collected = append(collected, s.Groups[0].Resources[0].Charges)
	}
	if generations != 1 {
		t.Fatalf("report generated %d times, want once", generations)
	}
	if !reflect.DeepEqual(collected[0], collected[1]) || len(collected[0]) != 2 {
		t.Fatalf("cached report did not reproduce the charges: %+v", collected)
	}
	window := filepath.Join(cache, billingTestSub, "2026-09-17_2026-09-18")
	info, err := os.Stat(filepath.Join(window, billingCachePart(0)))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("cached report parts must be private: %v %v", info, err)
	}
}

func TestBillingCacheRetriesAfterFailedGeneration(t *testing.T) {
	cache := t.TempDir()
	generations, failBlob := 0, true
	client := billingCacheClient(t, &generations, &failBlob)
	s := billingSnapshot()
	CollectWithCache(context.Background(), client, billingCredential{}, s, cache)
	if !s.HasErrors() || s.Groups[0].BillingStatus != "unavailable" {
		t.Fatalf("failed blob must fail the window: %+v", s.Groups[0])
	}
	window := filepath.Join(cache, billingTestSub, "2026-09-17_2026-09-18")
	if _, err := os.Stat(filepath.Join(window, billingCacheComplete)); err == nil {
		t.Fatal("a failed generation must not be marked complete")
	}
	if _, err := os.Stat(filepath.Join(window, billingCachePart(0))); err == nil {
		t.Fatal("a failed generation must not leave partial report parts")
	}
	failBlob = false
	s = billingSnapshot()
	CollectWithCache(context.Background(), client, billingCredential{}, s, cache)
	if s.HasErrors() || generations != 2 {
		t.Fatalf("collection after a failure must regenerate: generations=%d %+v", generations, s.Diagnostics)
	}
}
