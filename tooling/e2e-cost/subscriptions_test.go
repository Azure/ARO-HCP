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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

func TestResolveSubscriptionNames(t *testing.T) {
	other := "11111111-1111-1111-1111-111111111111"
	s := &Snapshot{
		Subscriptions: []SubscriptionSummary{{ID: billingTestSub, BillingStatus: "complete", TotalUSD: 7}, {ID: other}, {ID: strings.ToUpper(billingTestSub)}},
		Groups:        []Group{{SubscriptionID: "22222222-2222-2222-2222-222222222222"}},
	}
	calls := 0
	client := &http.Client{Transport: billingTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		id := strings.TrimPrefix(r.URL.Path, "/subscriptions/")
		if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "management.azure.com" || (id != billingTestSub && id != other) || r.URL.RawQuery != "api-version=2022-12-01" || r.Header.Get("Authorization") != "Bearer FAKE-TOKEN" {
			t.Fatalf("unexpected metadata request: %s %s", r.Method, r.URL)
		}
		if deadline, ok := r.Context().Deadline(); !ok || time.Until(deadline) > 30*time.Second {
			t.Fatal("metadata lookup must have a bounded deadline")
		}
		return billingResponse(200, fmt.Sprintf(`{"subscriptionId":%q,"displayName":"Same name"}`, strings.ToUpper(id))), nil
	})}
	ResolveSubscriptionNames(context.Background(), client, billingCredential{}, s)
	if calls != 2 || len(s.Diagnostics) != 0 || s.Subscriptions[0].TotalUSD != 7 || s.Subscriptions[0].BillingStatus != "complete" {
		t.Fatalf("lookups changed billing or escaped explicit scope: calls=%d snapshot=%+v", calls, s)
	}
	for _, sub := range s.Subscriptions {
		if sub.DisplayName != "Same name" {
			t.Fatalf("duplicate names or IDs lost metadata: %+v", sub)
		}
	}
	encoded, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var persisted Snapshot
	if err := json.Unmarshal(encoded, &persisted); err != nil || !reflect.DeepEqual(persisted.Subscriptions, s.Subscriptions) {
		t.Fatalf("names did not survive snapshot round trip: %v", err)
	}
	// A job's group subscriptions must never trigger account metadata discovery.
	s.Subscriptions = nil
	ResolveSubscriptionNames(context.Background(), client, billingCredential{}, s)
	if calls != 2 {
		t.Fatal("looked up subscriptions inferred from groups")
	}
}

func TestResolveSubscriptionNamesFallback(t *testing.T) {
	for _, failure := range []string{"forbidden", "transport", "authentication", "no credential", "invalid ID", "conflict", "missing ID", "missing name", "blank name", "malformed", "redirect", "cancelled", "timeout", "retry"} {
		t.Run(failure, func(t *testing.T) {
			s := &Snapshot{Subscriptions: []SubscriptionSummary{{ID: billingTestSub, DisplayName: "stale", BillingStatus: "complete", TotalUSD: 7, RetainedUSD: 5, ExcludedUSD: 2}}}
			var credential azcore.TokenCredential = billingCredential{}
			if failure == "authentication" {
				credential = billingCredential{err: errors.New("SECRET")}
			}
			if failure == "no credential" {
				credential = nil
			}
			if failure == "invalid ID" {
				s.Subscriptions[0].ID = "../SECRET?token=SECRET"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if failure == "cancelled" {
				cancel()
			}
			if failure == "timeout" {
				var timeoutCancel context.CancelFunc
				ctx, timeoutCancel = context.WithTimeout(ctx, 10*time.Millisecond)
				defer timeoutCancel()
			}
			calls := 0
			client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
				t.Fatal("caller redirect policy used")
				return nil
			}, Transport: billingTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "management.azure.com" || r.URL.Path != "/subscriptions/"+billingTestSub {
					t.Fatalf("credential leaked to redirect: %s", r.URL)
				}
				switch failure {
				case "transport":
					return nil, errors.New("SECRET")
				case "timeout":
					<-r.Context().Done()
					return nil, r.Context().Err()
				case "retry":
					return billingResponse(503, "SECRET"), nil
				case "redirect":
					resp := billingResponse(302, "SECRET")
					resp.Header.Set("Location", "https://management.azure.com/SECRET")
					return resp, nil
				case "conflict":
					return billingResponse(200, `{"subscriptionId":"11111111-1111-1111-1111-111111111111","displayName":"SECRET"}`), nil
				case "missing ID":
					return billingResponse(200, `{"displayName":"SECRET"}`), nil
				case "missing name":
					return billingResponse(200, fmt.Sprintf(`{"subscriptionId":%q}`, billingTestSub)), nil
				case "blank name":
					return billingResponse(200, fmt.Sprintf(`{"subscriptionId":%q,"displayName":" \t"}`, billingTestSub)), nil
				case "malformed":
					return billingResponse(200, `{"SECRET`), nil
				default:
					return billingResponse(403, "SECRET"), nil
				}
			})}
			ResolveSubscriptionNames(ctx, client, credential, s)
			wantCalls := 1
			switch failure {
			case "authentication", "no credential", "invalid ID", "cancelled":
				wantCalls = 0
			case "retry":
				wantCalls = 4
			case "timeout":
				wantCalls = calls // The deadline can expire before reaching the transport.
			}
			sub := s.Subscriptions[0]
			if calls != wantCalls || sub.DisplayName != "" || sub.BillingStatus != "complete" || sub.TotalUSD != 7 || sub.RetainedUSD != 5 || sub.ExcludedUSD != 2 || s.HasErrors() || len(s.Diagnostics) != 1 || !billingHasDiagnostic(s, "subscription-name-unavailable", "info") {
				t.Fatalf("optional lookup failure changed billing or lost diagnostic: calls=%d snapshot=%+v", calls, s)
			}
			if strings.Contains(fmt.Sprint(s.Diagnostics), "SECRET") {
				t.Fatal("metadata lookup diagnostic leaked request or response details")
			}
		})
	}
}

func TestDefaultExcludedGroup(t *testing.T) {
	for _, name := range []string{
		"hcp-underlay-ci00-j1234567", "HCP-UNDERLAY-CI01-J1234567-SVC-aks1", "hcp-underlay-ci01-j1234567-mgmt-2-aks1",
		"arbitrary-test-bcdfgh245678", "rg-nightly-4-20-bcdfgh245678--managed", "rg-test-bcdfgh245678-managed-2",
		"rg-test-bcdfgh245678-bcdfgh--managed", "rg-test-bcdfgh245678-nightly-bcdfgh--managed",
		"rg-test-bcdfgh245678-cp-ystream-bcdfgh--managed", "rg-" + strings.Repeat("x", 52) + "-1234abcd",
		"rg-cluster-back-version-zdlf7lhgr6df-4-20--managed", "rg-test-bcdfgh245678-4-20-1-managed-2",
	} {
		if reason := DefaultExcludedGroup(name); !strings.HasPrefix(reason, "Inferred") {
			t.Errorf("%q should be excluded with inferred provenance, got %q", name, reason)
		}
	}
	for _, name := range []string{
		"", "global", "shared", "production-cluster", "test", "rg-test", "rg-1234abcd", "unknown-" + strings.Repeat("x", 47) + "-1234abcd",
		"hcp-underlay-ci02-j1234567", "hcp-underlay-ci00-j123456", "hcp-underlay-ci01-j1234567-extra",
		"rg-abcdefghijkl", "rg-bcdfgh245678-extra", "rg-bcdfgh24567", "rg-bcdfgh2456789",
		"aro-hcp-msi-container-bcdfgh245678", "ARO-HCP-MSI-CONTAINER-bcdfgh245678--managed",
		"OIDC-WI-SJRVZ4--MANAGED", "rg-test-bcdfgh245678-arbitrary--managed", "rg-test-bcdfgh245678-4-foo--managed",
	} {
		if reason := DefaultExcludedGroup(name); reason != "" {
			t.Errorf("%q should be retained, got %q", name, reason)
		}
	}
}

func TestSubscriptionsValidationBeforeNetwork(t *testing.T) {
	for _, tc := range []struct {
		name, start, end      string
		subs, excludes, keeps []string
		noCredential          bool
	}{
		{name: "no subscriptions"},
		{name: "invalid subscription", subs: []string{billingTestSub, "not-a-uuid"}},
		{name: "empty subscription", subs: []string{""}},
		{name: "start only", subs: []string{billingTestSub}, start: "2026-09-17"},
		{name: "end only", subs: []string{billingTestSub}, end: "2026-09-17"},
		{name: "invalid date", subs: []string{billingTestSub}, start: "2026-02-30", end: "2026-09-17"},
		{name: "reversed", subs: []string{billingTestSub}, start: "2026-09-18", end: "2026-09-17"},
		{name: "future", subs: []string{billingTestSub}, start: "2026-09-18", end: "2026-09-19"},
		{name: "history", subs: []string{billingTestSub}, start: "2020-09-18", end: "2026-09-18"},
		{name: "bad exclude", subs: []string{billingTestSub}, excludes: []string{"["}},
		{name: "bad keep", subs: []string{billingTestSub}, keeps: []string{"["}},
		{name: "no credential", subs: []string{billingTestSub}, noCredential: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: billingTransport(func(*http.Request) (*http.Response, error) {
				t.Fatal("validation made a network request")
				return nil, nil
			})}
			var s *Snapshot
			if tc.noCredential {
				s = CollectSubscriptions(context.Background(), client, nil, tc.subs, tc.start, tc.end, tc.excludes, tc.keeps, billingSnapshot().CollectedAt)
			} else {
				s = CollectSubscriptions(context.Background(), client, billingCredential{err: errors.New("must not authenticate")}, tc.subs, tc.start, tc.end, tc.excludes, tc.keeps, billingSnapshot().CollectedAt)
			}
			if !s.HasErrors() || s.Mode != "subscriptions" || s.Currency != "USD" || s.CostBasis != "AmortizedCost" || s.Version != SchemaVersion || s.Job != (Job{}) {
				t.Fatalf("validation not persisted as a subscription snapshot: %+v", s)
			}
			encoded, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			var persisted Snapshot
			if err := json.Unmarshal(encoded, &persisted); err != nil {
				t.Fatal(err)
			}
			if !persisted.HasErrors() {
				t.Fatal("validation errors lost on disk")
			}
		})
	}
}

func TestSubscriptionsDefaultUTCAndDedup(t *testing.T) {
	// Local September 18 is already September 19 in UTC.
	now := time.Date(2026, 9, 18, 23, 30, 0, 0, time.FixedZone("west", -7*3600))
	calls := 0
	client := &http.Client{Transport: billingTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var body struct {
			Metric     string
			TimePeriod billingWindow
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if r.URL.Host != "management.azure.com" || r.URL.Path != "/subscriptions/"+billingTestSub+"/providers/Microsoft.CostManagement/generateCostDetailsReport" || body.Metric != "AmortizedCost" || body.TimePeriod != (billingWindow{"2026-09-16", "2026-09-17"}) {
			t.Fatalf("unexpected subscription request: %s %+v", r.URL, body)
		}
		return billingResponse(204, ""), nil
	})}
	s := CollectSubscriptions(context.Background(), client, billingCredential{}, []string{billingTestSub, strings.ToUpper(billingTestSub)}, "", "", nil, nil, now)
	if s.HasErrors() || calls != 1 || len(s.Subscriptions) != 1 || s.Subscriptions[0].BillingStatus != "complete" || s.Subscriptions[0].TotalUSD != 0 || s.QueryStart != "2026-09-16" || s.QueryEnd != "2026-09-17" || s.CollectedAt.Location() != time.UTC || !billingHasDiagnostic(s, "billing-no-records", "info") {
		t.Fatalf("incorrect default/dedup/empty report: %+v", s)
	}
}

func TestSubscriptionsFiltersReconcile(t *testing.T) {
	data := billingTestHeader
	for _, row := range []struct{ name, amount string }{
		{"shared", "10"}, {"SHARED", "-2"}, {"hcp-underlay-ci01-j1234567-svc-aks1", "4"},
		{"rg-test-bcdfgh245678--managed", "-3"}, {"custom-drop", "5"}, {"rg-keep-bcdfgh245678", "6"},
		{"aro-hcp-msi-container-bcdfgh245678", "7"}, {"global", "0"},
	} {
		data += billingRow(row.name, "", "2026-09-17", row.amount)
	}
	s := CollectSubscriptions(context.Background(), billingCSVClient(t, data), billingCredential{}, []string{billingTestSub}, "2026-09-17", "2026-09-18", []string{"^custom-", "^rg-keep-"}, []string{"^rg-keep-"}, billingSnapshot().CollectedAt)
	if s.HasErrors() || len(s.Groups) != 4 || len(s.FilteredGroups) != 3 {
		t.Fatalf("incorrect filtering: %+v", s)
	}
	summary := s.Subscriptions[0]
	if summary.TotalUSD != 27 || summary.RetainedUSD != 21 || summary.ExcludedUSD != 6 || summary.TotalUSD != summary.RetainedUSD+summary.ExcludedUSD {
		t.Fatalf("signed totals do not reconcile: %+v", summary)
	}
	retained, excluded := 0.0, 0.0
	for _, g := range s.Groups {
		if g.BillingStatus != "complete" {
			t.Fatalf("unexpected group status: %+v", g)
		}
		for _, r := range g.Resources {
			for _, c := range r.Charges {
				retained += c.CostUSD
			}
		}
	}
	for _, g := range s.FilteredGroups {
		excluded += g.CostUSD
		if g.SubscriptionID != billingTestSub || g.Reason == "" {
			t.Fatalf("exclusion not auditable: %+v", g)
		}
	}
	if retained != summary.RetainedUSD || excluded != summary.ExcludedUSD {
		t.Fatal("audit and retained inventory do not reconcile")
	}
	// Keeping everything is an escape hatch for all naming defaults and custom rules.
	s = CollectSubscriptions(context.Background(), billingCSVClient(t, data), billingCredential{}, []string{billingTestSub}, "2026-09-17", "2026-09-18", []string{".*"}, []string{".*"}, billingSnapshot().CollectedAt)
	if s.HasErrors() || len(s.FilteredGroups) != 0 || s.Subscriptions[0].RetainedUSD != 27 {
		t.Fatalf("keep did not override all exclusions: %+v", s)
	}
}

func TestSubscriptionsPurchasesAndMCA(t *testing.T) {
	header := "SubscriptionId,resourceGroupName,ResourceId,date,costInBillingCurrency,billingCurrency,costInUsd,meterCategory\n"
	data := header + strings.Join([]string{
		billingTestSub + ",,Purchase:RESERVATION-A,2026-09-17,100,EUR,1.25,Reservations",
		billingTestSub + ",null,Purchase:B,2026-09-17,-2,USD,,",
		billingTestSub + ",notapplicable,,2026-09-17,3,USD,,",
		billingTestSub + ",(No resource group),,2026-09-17,4,USD,,",
		billingTestSub + ",owned," + billingTestID + ",2026-09-17,5,USD,,",
	}, "\n") + "\n"
	s := CollectSubscriptions(context.Background(), billingCSVClient(t, data), billingCredential{}, []string{billingTestSub}, "2026-09-17", "2026-09-18", nil, nil, billingSnapshot().CollectedAt)
	if s.HasErrors() || len(s.Groups) != 5 || s.Subscriptions[0].TotalUSD != 11.25 {
		t.Fatalf("purchases lost or literal groups merged: %+v", s)
	}
	r := s.Groups[0].Resources[0]
	if s.Groups[0].Name != "(No resource group)" || r.ID != "Purchase:RESERVATION-A" || r.Name != r.ID || r.Type != "Reservations" {
		t.Fatalf("non-ARM purchase identity lost: %+v", s.Groups[0])
	}
	if s.Groups[1].Name != "null" || s.Groups[1].Resources[0].Type != "Unknown" || s.Groups[2].Name != "notapplicable" || s.Groups[2].Resources[0].ID != "" {
		t.Fatalf("literal or missing identity lost: %+v", s.Groups)
	}
	r = s.Groups[4].Resources[0]
	if r.Name != "agent" || r.Type != "Microsoft.Compute/virtualMachines/extensions" {
		t.Fatalf("ARM type extraction failed: %+v", r)
	}
}

func TestSubscriptionsMonthsPartialAndIsolation(t *testing.T) {
	otherSub := "11111111-1111-1111-1111-111111111111"
	for _, failure := range []string{"none", "HTTP", "last blob", "CSV", "currency", "row subscription", "ARM subscription", "malformed ARM subscription", "group conflict"} {
		t.Run(failure, func(t *testing.T) {
			var windows []billingWindow
			sub, window := "", billingWindow{}
			client := &http.Client{Transport: billingTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "management.azure.com" {
					sub = strings.Split(r.URL.Path, "/")[2]
					var body struct{ TimePeriod billingWindow }
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					window = body.TimePeriod
					windows = append(windows, window)
					if sub == billingTestSub && window.Start == "2026-09-01" {
						if failure == "HTTP" {
							return billingResponse(403, "SECRET"), nil
						}
						if failure == "last blob" {
							return billingResponse(200, billingManifest(billingTestBlob, billingTestBlob+"2")), nil
						}
					}
					return billingResponse(200, billingManifest(billingTestBlob)), nil
				}
				if strings.HasSuffix(r.URL.String(), "2") {
					return nil, errors.New("SECRET")
				}
				data := billingTestHeader + billingRow("shared", "", "2026-08-30", "999") + billingRow("shared", "", "2026-08-31", "1") + billingRow("shared", "", "2026-09-01", "2") + billingRow("shared", "", "2026-09-02", "999")
				if sub == billingTestSub && window.Start == "2026-09-01" {
					switch failure {
					case "CSV":
						data += "shared,\"unterminated\n"
					case "currency":
						data += strings.ReplaceAll(billingRow("shared", "", "2026-09-01", "999"), "USD", "EUR")
					case "row subscription":
						data += strings.ReplaceAll(billingRow("shared", "", "2026-09-01", "999"), billingTestSub, otherSub)
					case "ARM subscription":
						data += billingRow("shared", strings.ReplaceAll(billingTestID, billingTestSub, otherSub), "2026-09-01", "999")
					case "malformed ARM subscription":
						data += billingRow("shared", "/subscriptions/"+otherSub+"/broken", "2026-09-01", "999")
					case "group conflict":
						data += billingRow("shared", billingTestID, "2026-09-01", "4")
					}
				}
				if sub == otherSub {
					data = strings.ReplaceAll(data, billingTestSub, otherSub)
				}
				return billingResponse(200, data), nil
			})}
			s := CollectSubscriptions(context.Background(), client, billingCredential{}, []string{billingTestSub, otherSub}, "2026-08-31", "2026-09-01", nil, nil, billingSnapshot().CollectedAt)
			wantWindows := []billingWindow{{"2026-08-31", "2026-08-31"}, {"2026-09-01", "2026-09-01"}, {"2026-08-31", "2026-08-31"}, {"2026-09-01", "2026-09-01"}}
			if !reflect.DeepEqual(windows, wantWindows) || len(s.Groups) != 2 || s.Groups[0].SubscriptionID == s.Groups[1].SubscriptionID {
				t.Fatalf("bad boundaries or subscription bleed: %+v %+v", windows, s)
			}
			want, status := 3.0, "complete"
			if failure != "none" {
				status = "partial"
			}
			if failure == "HTTP" {
				want = 1
			}
			if failure == "group conflict" {
				want = 7
			}
			if s.Subscriptions[0].TotalUSD != want || s.Subscriptions[0].BillingStatus != status || s.Subscriptions[1].TotalUSD != 3 || s.Subscriptions[1].BillingStatus != "complete" || s.HasErrors() != (failure != "none") {
				t.Fatalf("partial costs lost or errors hidden: %+v", s)
			}
			encoded, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "SECRET") {
				t.Fatal("transport details leaked")
			}
		})
	}
}

func TestSubscriptionsAllFailedAndEmpty(t *testing.T) {
	for _, status := range []int{200, 204, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			client := &http.Client{Transport: billingTransport(func(*http.Request) (*http.Response, error) { return billingResponse(status, billingManifest()), nil })}
			s := CollectSubscriptions(context.Background(), client, billingCredential{}, []string{billingTestSub}, "2026-09-17", "2026-09-18", nil, nil, billingSnapshot().CollectedAt)
			want := "complete"
			if status == 403 {
				want = "unavailable"
			}
			if s.Subscriptions[0].BillingStatus != want || s.HasErrors() != (status == 403) || len(s.Groups) != 0 || s.Subscriptions[0].TotalUSD != 0 {
				t.Fatalf("incorrect no-data status: %+v", s)
			}
		})
	}
}

func TestSubscriptionsValidRowsSurviveInvalidRows(t *testing.T) {
	for _, value := range []string{"NaN", "+Inf", "1e999", "", "not-a-number"} {
		t.Run(value, func(t *testing.T) {
			data := billingTestHeader + billingRow("shared", "", "2026-09-17", "0") + billingRow("shared", "", "2026-09-17", value) + billingRow("rg-test-bcdfgh245678", "", "2026-09-18", "-2")
			s := CollectSubscriptions(context.Background(), billingCSVClient(t, data), billingCredential{}, []string{billingTestSub}, "2026-09-17", "2026-09-18", nil, nil, billingSnapshot().CollectedAt)
			if !s.HasErrors() || s.Subscriptions[0].BillingStatus != "partial" || s.Subscriptions[0].TotalUSD != -2 || s.Subscriptions[0].RetainedUSD != 0 || s.Subscriptions[0].ExcludedUSD != -2 || len(s.Groups) != 1 || len(s.Groups[0].Resources) != 1 || s.Groups[0].Resources[0].Charges[0].CostUSD != 0 || len(s.FilteredGroups) != 1 {
				t.Fatalf("valid zero/negative rows lost after malformed row: %+v", s)
			}
		})
	}
	data := billingTestHeader + billingRow("outside", "", "2026-09-16", "999")
	s := CollectSubscriptions(context.Background(), billingCSVClient(t, data), billingCredential{}, []string{billingTestSub}, "2026-09-17", "2026-09-18", nil, nil, billingSnapshot().CollectedAt)
	if s.HasErrors() || len(s.Groups) != 0 || s.Subscriptions[0].BillingStatus != "complete" || !billingHasDiagnostic(s, "billing-no-records", "info") {
		t.Fatalf("out-of-window group leaked into snapshot: %+v", s)
	}
}

func TestSubscriptionsExcludedRecordsRequireValidCharge(t *testing.T) {
	name := "rg-test-bcdfgh245678"
	for _, valid := range []bool{false, true} {
		t.Run(fmt.Sprint(valid), func(t *testing.T) {
			data := billingTestHeader + billingRow(name, "", "2026-09-17", "NaN")
			if valid {
				data += billingRow(name, "", "2026-09-17", "0")
			}
			s := CollectSubscriptions(context.Background(), billingCSVClient(t, data), billingCredential{}, []string{billingTestSub}, "2026-09-17", "2026-09-18", nil, nil, billingSnapshot().CollectedAt)
			wantStatus, wantGroups := "unavailable", 0
			if valid {
				wantStatus, wantGroups = "partial", 1
			}
			if !s.HasErrors() || len(s.FilteredGroups) != wantGroups || len(s.Groups) != 0 || s.Subscriptions[0].BillingStatus != wantStatus || s.Subscriptions[0].TotalUSD != 0 {
				t.Fatalf("malformed exclusion became a zero charge or valid zero lost: %+v", s)
			}
			found := false
			for _, d := range s.Diagnostics {
				found = found || d.Code == "billing-window" && d.Scope == billingTestSub+"/"+name
			}
			if !found {
				t.Fatal("malformed excluded group's diagnostic scope lost")
			}
		})
	}
}

func TestSubscriptionsIndependentBlobs(t *testing.T) {
	for _, failure := range []string{"HTTP", "transport", "CSV", "header"} {
		for _, job := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/job=%t", failure, job), func(t *testing.T) {
				blobs := 0
				client := &http.Client{Transport: billingTransport(func(r *http.Request) (*http.Response, error) {
					if r.URL.Host == "management.azure.com" {
						return billingResponse(200, billingManifest(billingTestBlob, billingTestBlob+"2", billingTestBlob+"3")), nil
					}
					blobs++
					if blobs == 1 {
						switch failure {
						case "HTTP":
							return billingResponse(403, "SECRET"), nil
						case "transport":
							return nil, errors.New("SECRET")
						case "CSV":
							return billingResponse(200, billingTestHeader+"owned,\"unterminated"), nil
						case "header":
							return billingResponse(200, "ResourceGroup\n"), nil
						}
					}
					if blobs == 2 {
						return billingResponse(200, billingTestHeader+billingRow("owned", "", "2026-09-17", "7")), nil
					}
					return billingResponse(404, "SECRET"), nil
				})}
				if job {
					s := billingSnapshot()
					Collect(context.Background(), client, billingCredential{}, s)
					if blobs != 1 || !s.HasErrors() || s.Groups[0].BillingStatus != "unavailable" || len(s.Groups[0].Resources) != 0 {
						t.Fatalf("job mode no longer fails fast: %+v", s)
					}
					return
				}
				s := CollectSubscriptions(context.Background(), client, billingCredential{}, []string{billingTestSub}, "2026-09-17", "2026-09-18", nil, nil, billingSnapshot().CollectedAt)
				if blobs != 3 || !s.HasErrors() || s.Subscriptions[0].BillingStatus != "partial" || s.Subscriptions[0].TotalUSD != 7 || len(s.Groups) != 1 {
					t.Fatalf("independent successful blob lost: %+v", s)
				}
				encoded, err := json.Marshal(s)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(encoded), "SECRET") || !strings.Contains(string(encoded), "HTTP 404") {
					t.Fatal("blob errors leaked secrets or lost later failure")
				}
			})
		}
	}
}

func TestSubscriptionsClassificationAfterScopeValidation(t *testing.T) {
	for _, first := range []string{"outside", "date", "ID", "fields"} {
		for _, keep := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/keep=%t", first, keep), func(t *testing.T) {
				row := billingRow("SHARED", "", "2026-09-16", "99")
				switch first {
				case "date":
					row = billingRow("SHARED", "", "invalid", "99")
				case "ID":
					row = billingRow("SHARED", "/subscriptions/11111111-1111-1111-1111-111111111111/broken", "2026-09-17", "99")
				case "fields":
					row = billingTestSub + ",SHARED\n"
				}
				var keeps []string
				if keep {
					keeps = []string{"^shared$"}
				}
				data := billingTestHeader + row + billingRow("shared", "", "2026-09-17", "1") + billingRow("SHARED", "", "2026-09-18", "2")
				s := CollectSubscriptions(context.Background(), billingCSVClient(t, data), billingCredential{}, []string{billingTestSub}, "2026-09-17", "2026-09-18", []string{"^shared$"}, keeps, billingSnapshot().CollectedAt)
				if s.HasErrors() != (first != "outside") || s.Subscriptions[0].TotalUSD != 3 {
					t.Fatalf("invalid row changed scoped costs: %+v", s)
				}
				if keep {
					if len(s.Groups) != 1 || s.Groups[0].Name != "shared" || s.Groups[0].Category != "Residual" || s.Groups[0].Kind != "retained" || len(s.FilteredGroups) != 0 {
						t.Fatalf("early row established group or normalized keep failed: %+v", s)
					}
				} else if len(s.FilteredGroups) != 1 || s.FilteredGroups[0].Name != "shared" || s.FilteredGroups[0].CostUSD != 3 || len(s.Groups) != 0 {
					t.Fatalf("early row established group or normalized exclude failed: %+v", s)
				}
				for _, d := range s.Diagnostics {
					if d.Severity == "error" && d.Scope != billingTestSub {
						t.Fatalf("invalid-scope row classified a group: %+v", d)
					}
				}
			})
		}
	}
	// Patterns are not rewritten: only names are normalized to lowercase.
	for _, pattern := range []string{"^shared$", "^SHARED$", "(?i)^SHARED$"} {
		data := billingTestHeader + billingRow("SHARED", "", "2026-09-17", "1")
		s := CollectSubscriptions(context.Background(), billingCSVClient(t, data), billingCredential{}, []string{billingTestSub}, "2026-09-17", "2026-09-18", []string{pattern}, nil, billingSnapshot().CollectedAt)
		if s.HasErrors() || (len(s.FilteredGroups) == 1) != (pattern != "^SHARED$") {
			t.Fatalf("incorrect normalized regex semantics for %q: %+v", pattern, s)
		}
	}
}

func TestSubscriptionsEmptyCSVSchema(t *testing.T) {
	for _, tc := range []struct {
		header string
		valid  bool
	}{
		{"ResourceGroup", false},
		{"ResourceGroup,CostInUsd", false},
		{"ResourceGroup,Date", false},
		{"ResourceGroup,Date,Cost", false},
		{"ResourceGroup,Date,CostInBillingCurrency,PricingCurrency", false},
		{"ResourceGroup,Date,CostInPricingCurrency,BillingCurrency", false},
		{"ResourceGroup,Date,CostInUsd", true},
		{"ResourceGroup,Date,Cost,Currency", true},
		{"ResourceGroupName,UsageDateTime,CostInBillingCurrency,BillingCurrency", true},
		{"ResourceGroup,Date,Cost,BillingCurrencyCode", true},
	} {
		t.Run(tc.header, func(t *testing.T) {
			s := CollectSubscriptions(context.Background(), billingCSVClient(t, tc.header+"\n"), billingCredential{}, []string{billingTestSub}, "2026-09-17", "2026-09-18", nil, nil, billingSnapshot().CollectedAt)
			want := "unavailable"
			if tc.valid {
				want = "complete"
			}
			if s.HasErrors() == tc.valid || s.Subscriptions[0].BillingStatus != want || len(s.Groups) != 0 || len(s.FilteredGroups) != 0 || billingHasDiagnostic(s, "billing-no-records", "info") != tc.valid {
				t.Fatalf("empty CSV schema incorrectly accepted/rejected: %+v", s)
			}
		})
	}
}
