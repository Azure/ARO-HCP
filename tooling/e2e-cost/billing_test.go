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
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const billingTestSub = "974ebd46-8ad3-41e3-afef-7ef25fd5c371"
const billingTestID = "/subscriptions/" + billingTestSub + "/resourceGroups/owned/providers/Microsoft.Compute/virtualMachines/vm/extensions/agent"
const billingTestBlob = "https://reports.blob.core.windows.net/cost/part?sig=SECRET"
const billingTestHeader = "SubscriptionId,ResourceGroup,ResourceId,Date,Cost,BillingCurrency,MeterId,MeterName,MeterCategory\n"

type billingTransport func(*http.Request) (*http.Response, error)

func (f billingTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type billingCredential struct{ err error }

func (c billingCredential) GetToken(_ context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	if !reflect.DeepEqual(options.Scopes, []string{"https://management.azure.com/.default"}) {
		return azcore.AccessToken{}, errors.New("wrong token scope")
	}
	return azcore.AccessToken{Token: "FAKE-TOKEN", ExpiresOn: time.Now().Add(time.Hour)}, c.err
}

func billingResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"0"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func billingManifest(links ...string) string {
	blobs := make([]map[string]string, 0, len(links))
	for _, link := range links {
		blobs = append(blobs, map[string]string{"blobLink": link})
	}
	b, _ := json.Marshal(map[string]any{"status": "Completed", "manifest": map[string]any{"dataFormat": "Csv", "blobCount": len(blobs), "blobs": blobs}})
	return string(b)
}

func billingSnapshot() *Snapshot {
	return &Snapshot{CollectedAt: time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC), QueryStart: "2026-09-17", QueryEnd: "2026-09-18", Groups: []Group{{SubscriptionID: billingTestSub, Name: "owned", Owner: "test", BillingStatus: "pending"}}}
}

func billingRow(rg, id, day, cost string) string {
	return strings.Join([]string{billingTestSub, rg, id, day, cost, "USD", "meter", "Compute", "Virtual Machines"}, ",") + "\n"
}

func billingHasDiagnostic(s *Snapshot, code, severity string) bool {
	for _, d := range s.Diagnostics {
		if d.Code == code && d.Severity == severity {
			return true
		}
	}
	return false
}

func billingCSVClient(t *testing.T, data string) *http.Client {
	t.Helper()
	return &http.Client{Transport: billingTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "management.azure.com" {
			if r.Header.Get("Authorization") != "Bearer FAKE-TOKEN" {
				t.Fatal("ARM request has no token")
			}
			return billingResponse(200, billingManifest(billingTestBlob)), nil
		}
		if r.URL.String() != billingTestBlob || r.Header.Get("Authorization") != "" {
			t.Fatal("unexpected blob request or leaked bearer")
		}
		return billingResponse(200, data), nil
	})}
}

func TestBillingAsyncPartitionsAndAttribution(t *testing.T) {
	s := billingSnapshot()
	s.Groups[0].Resources = []Resource{{ID: strings.ToUpper(billingTestID), Name: "agent", Source: "artifact"}}
	s.Groups = append(s.Groups, Group{SubscriptionID: billingTestSub, Name: "empty", BillingStatus: "pending"})
	requests := 0
	client := &http.Client{Transport: billingTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.Host == "management.azure.com" && r.Header.Get("Authorization") != "Bearer FAKE-TOKEN" {
			t.Fatal("missing ARM bearer")
		}
		switch requests {
		case 1:
			if r.Method != "POST" || r.URL.Path != "/subscriptions/"+billingTestSub+"/providers/Microsoft.CostManagement/generateCostDetailsReport" || r.URL.Query().Get("api-version") != "2025-03-01" {
				t.Fatalf("incorrect create request: %s", r.URL.Path)
			}
			var body struct {
				Metric     string
				TimePeriod struct{ Start, End string }
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Metric != "AmortizedCost" || body.TimePeriod.Start != s.QueryStart || body.TimePeriod.End != s.QueryEnd {
				t.Fatalf("wrong request body: %+v", body)
			}
			resp := billingResponse(202, "")
			resp.Header.Set("Location", "https://management.azure.com/poll/one")
			return resp, nil
		case 2:
			if r.Method != "GET" || r.URL.Path != "/poll/one" {
				t.Fatal("wrong initial poll")
			}
			resp := billingResponse(202, "")
			resp.Header.Set("Location", "https://management.azure.com/poll/two")
			return resp, nil
		case 3:
			if r.Method != "GET" || r.URL.Path != "/poll/two" {
				t.Fatal("updated location not followed")
			}
			return billingResponse(200, billingManifest(billingTestBlob, billingTestBlob+"2")), nil
		case 4, 5:
			if r.Header.Get("Authorization") != "" || r.Method != "GET" {
				t.Fatal("blob download leaked bearer token")
			}
			data := billingTestHeader + billingRow("OWNED", billingTestID, "09/17/2026", "0.123456789")
			if requests == 4 {
				data += billingRow("owned", billingTestID, "2026-09-17", "-0.1")
				data += billingRow("owned", billingTestID, "2026-09-18", "0")
				data += billingRow("owned", "", "2026-09-18", "-2")
				data += billingRow("unowned", "", "invalid", "NaN")
				data += strings.ReplaceAll(billingRow("owned", "", "2026-09-18", "999"), billingTestSub, "11111111-1111-1111-1111-111111111111")
				data += billingRow("owned", "", "2026-09-16", "999")
				data += billingRow("owned", "", "2026-09-19", "999")
			}
			return billingResponse(200, data), nil
		default:
			t.Fatal("unexpected request")
		}
		return nil, errors.New("unexpected request")
	})}
	Collect(context.Background(), client, billingCredential{}, s)
	if s.HasErrors() || requests != 5 || s.CostBasis != "AmortizedCost" || s.Currency != "USD" {
		t.Fatalf("unexpected result: %+v", s)
	}
	g := s.Groups[0]
	if g.BillingStatus != "complete" || len(g.Resources) != 2 {
		t.Fatalf("unexpected group: %+v", g)
	}
	r := g.Resources[0]
	if r.Source != "both" || r.Type != "Microsoft.Compute/virtualMachines/extensions" || len(r.Charges) != 2 {
		t.Fatalf("inventory not merged: %+v", r)
	}
	if r.Charges[0].CostUSD != (0.123456789-0.1)+0.123456789 || r.Charges[1].CostUSD != 0 {
		t.Fatalf("precision, duplicate rows, or explicit zero lost: %+v", r.Charges)
	}
	if g.Resources[1].Name != "Unspecified" || g.Resources[1].Type != "Virtual Machines" || g.Resources[1].Charges[0].CostUSD != -2 {
		t.Fatalf("missing-ID charge lost: %+v", g.Resources[1])
	}
	if !billingHasDiagnostic(s, "billing-no-records", "info") || !billingHasDiagnostic(s, "billing-provisional", "info") {
		t.Fatalf("missing informational diagnostics: %+v", s.Diagnostics)
	}
}

func TestBillingCSVVariants(t *testing.T) {
	for _, tc := range []struct {
		name, header, row string
		want              float64
		invalid           bool
	}{
		{"EA", "ResourceGroup,ResourceId,Date,Cost,Currency", "owned,,2026-09-17,-1.23456789,USD", -1.23456789, false},
		{"MCA", "ResourceGroup,ResourceId,Date,CostInBillingCurrency,BillingCurrency", "owned,,2026-09-17,0,USD", 0, false},
		{"Azure USD", "ResourceGroup,ResourceId,Date,CostInBillingCurrency,BillingCurrency,costInUsd", "owned,,2026-09-17,100,EUR,1.25", 1.25, false},
		{"legacy date and BOM", "\ufeffResource Group,InstanceId,UsageDateTime,Cost,BillingCurrencyCode", "owned,,2026-09-17T00:00:00Z,1,USD", 1, false},
		{"non USD", "ResourceGroup,ResourceId,Date,Cost,BillingCurrency", "owned,,2026-09-17,100,EUR", 0, true},
		{"no currency", "ResourceGroup,ResourceId,Date,Cost", "owned,,2026-09-17,100", 0, true},
		{"NaN", "ResourceGroup,ResourceId,Date,Cost,Currency", "owned,,2026-09-17,NaN,USD", 0, true},
		{"infinity", "ResourceGroup,ResourceId,Date,Cost,Currency", "owned,,2026-09-17,+Inf,USD", 0, true},
		{"overflow", "ResourceGroup,ResourceId,Date,Cost,Currency", "owned,,2026-09-17,1e999,USD", 0, true},
		{"bad date", "ResourceGroup,ResourceId,Date,Cost,Currency", "owned,,bad,1,USD", 0, true},
		{"bad ID", "ResourceGroup,ResourceId,Date,Cost,Currency", "owned,not-an-id,2026-09-17,1,USD", 0, true},
		{"wrong ID group", "ResourceGroup,ResourceId,Date,Cost,Currency", "owned," + strings.ReplaceAll(billingTestID, "/owned/", "/other/") + ",2026-09-17,1,USD", 0, true},
		{"short row", "ResourceGroup,ResourceId,Date,Cost,Currency", "owned,,2026-09-17", 0, true},
		{"extra field", "ResourceGroup,ResourceId,Date,Cost,Currency", "owned,,2026-09-17,1,USD,unexpected", 0, true},
		{"bad quoting", "ResourceGroup,ResourceId,Date,Cost,Currency", "owned,\"unterminated", 0, true},
		{"duplicate header", "ResourceGroup,Date,Cost,Currency,Cost", "owned,2026-09-17,1,USD,2", 0, true},
		{"missing ownership", "Date,Cost,Currency", "2026-09-17,1,USD", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := billingSnapshot()
			Collect(context.Background(), billingCSVClient(t, tc.header+"\n"+tc.row+"\n"), billingCredential{}, s)
			if tc.invalid {
				if !s.HasErrors() || s.Groups[0].BillingStatus != "unavailable" || len(s.Groups[0].Resources) != 0 {
					t.Fatalf("malformed row accepted: %+v", s)
				}
			} else if s.HasErrors() || s.Groups[0].BillingStatus != "complete" || len(s.Groups[0].Resources) != 1 || s.Groups[0].Resources[0].Charges[0].CostUSD != tc.want {
				t.Fatalf("incorrect charge: %+v", s)
			}
		})
	}
}

func TestBillingLiveMCAHeader(t *testing.T) {
	// Header verified against the live 2025-03-01 Cost Details API. All rows
	// below are synthetic; no live billing records or download URLs are kept.
	header := strings.Split("\ufeffinvoiceId,previousInvoiceId,billingAccountId,billingAccountName,billingProfileId,billingProfileName,invoiceSectionId,invoiceSectionName,resellerName,resellerMpnId,costCenter,billingPeriodEndDate,billingPeriodStartDate,servicePeriodEndDate,servicePeriodStartDate,date,serviceFamily,productOrderId,productOrderName,consumedService,meterId,meterName,meterCategory,meterSubCategory,meterRegion,ProductId,ProductName,SubscriptionId,subscriptionName,publisherType,publisherId,publisherName,resourceGroupName,ResourceId,resourceLocation,location,effectivePrice,quantity,unitOfMeasure,chargeType,billingCurrency,pricingCurrency,costInBillingCurrency,costInPricingCurrency,costInUsd,paygCostInBillingCurrency,paygCostInUsd,exchangeRatePricingToBilling,exchangeRateDate,isAzureCreditEligible,serviceInfo1,serviceInfo2,additionalInfo,tags,PayGPrice,frequency,term,reservationId,reservationName,pricingModel,unitPrice,costAllocationRuleName,benefitId,benefitName,provider", ",")
	for _, conflictingID := range []bool{false, true} {
		t.Run(fmt.Sprintf("conflicting-ID=%t", conflictingID), func(t *testing.T) {
			var data strings.Builder
			writer := csv.NewWriter(&data)
			if err := writer.Write(header); err != nil {
				t.Fatal(err)
			}
			for _, values := range []map[string]string{
				{"resourceGroupName": "OWNED", "ResourceId": billingTestID, "costInBillingCurrency": "0"},
				{"resourceGroupName": "owned", "billingCurrency": "EUR", "costInUsd": "-1.25"},
				{"resourceGroupName": "unowned", "costInBillingCurrency": "NaN"},
				{"resourceGroupName": "owned", "SubscriptionId": "11111111-1111-1111-1111-111111111111", "costInBillingCurrency": "NaN"},
			} {
				row := make([]string, len(header))
				defaults := map[string]string{"SubscriptionId": billingTestSub, "date": "09/17/2026", "billingCurrency": "USD", "meterId": "meter", "meterName": "Compute", "meterCategory": "Virtual Machines"}
				for i, column := range header {
					row[i] = defaults[column]
					if value, ok := values[column]; ok {
						row[i] = value
					}
					if conflictingID && column == "ResourceId" && row[i] != "" {
						row[i] = strings.ReplaceAll(row[i], "/owned/", "/other/")
					}
				}
				if err := writer.Write(row); err != nil {
					t.Fatal(err)
				}
			}
			writer.Flush()
			if err := writer.Error(); err != nil {
				t.Fatal(err)
			}
			s := billingSnapshot()
			Collect(context.Background(), billingCSVClient(t, data.String()), billingCredential{}, s)
			g := s.Groups[0]
			if conflictingID {
				if !s.HasErrors() || g.BillingStatus != "unavailable" || len(g.Resources) != 0 {
					t.Fatalf("resourceGroupName not used in ARM ID validation: %+v", s)
				}
				return
			}
			if s.HasErrors() || g.BillingStatus != "complete" || len(g.Resources) != 2 {
				t.Fatalf("live MCA schema not attributed correctly: %+v", s)
			}
			for _, r := range g.Resources {
				if len(r.Charges) != 1 || r.Charges[0].Day != "2026-09-17" {
					t.Fatalf("incorrect MCA charges: %+v", r)
				}
				if r.ID == "" {
					if r.Name != "Unspecified" || r.Charges[0].CostUSD != -1.25 {
						t.Fatalf("missing-ID USD amount lost: %+v", r)
					}
				} else if r.ID != strings.ToLower(billingTestID) || r.Charges[0].CostUSD != 0 {
					t.Fatalf("explicit zero or ARM ID lost: %+v", r)
				}
			}
		})
	}
}

func TestBillingMonthsPartialAndBoundaries(t *testing.T) {
	for _, failure := range []string{"none", "CSV", "HTTP", "blob", "last blob"} {
		t.Run(failure, func(t *testing.T) {
			s := billingSnapshot()
			s.QueryStart, s.QueryEnd = "2026-08-31", "2026-09-01"
			s.Groups = append(s.Groups, Group{SubscriptionID: billingTestSub, Name: "healthy"})
			var windows []billingWindow
			month := 0
			client := &http.Client{Transport: billingTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "management.azure.com" {
					month++
					var request struct{ TimePeriod billingWindow }
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Fatal(err)
					}
					windows = append(windows, request.TimePeriod)
					if month == 2 && failure == "HTTP" {
						return billingResponse(403, "SECRET"), nil
					}
					if month == 2 && failure == "last blob" {
						return billingResponse(200, billingManifest(billingTestBlob, billingTestBlob+"2")), nil
					}
					return billingResponse(200, billingManifest(billingTestBlob)), nil
				}
				if month == 2 && (failure == "blob" || failure == "last blob" && strings.HasSuffix(r.URL.RawQuery, "2")) {
					return nil, errors.New("SECRET")
				}
				data := billingTestHeader + billingRow("owned", "", "2026-08-31", "1") + billingRow("owned", "", "2026-09-01", "2") + billingRow("healthy", "", "2026-09-01", "3")
				if month == 2 && failure == "CSV" {
					data += billingRow("owned", "", "2026-09-01", "NaN")
				}
				return billingResponse(200, data), nil
			})}
			Collect(context.Background(), client, billingCredential{}, s)
			if !reflect.DeepEqual(windows, []billingWindow{{"2026-08-31", "2026-08-31"}, {"2026-09-01", "2026-09-01"}}) {
				t.Fatalf("wrong inclusive monthly requests: %+v", windows)
			}
			g := s.Groups[0]
			if failure == "none" {
				if g.BillingStatus != "complete" || len(g.Resources[0].Charges) != 2 || g.Resources[0].Charges[0].CostUSD != 1 || g.Resources[0].Charges[1].CostUSD != 2 {
					t.Fatalf("boundary data lost or doubled: %+v", g)
				}
			} else if g.BillingStatus != "partial" || len(g.Resources[0].Charges) != 1 || g.Resources[0].Charges[0].CostUSD != 1 {
				t.Fatalf("successful month lost or failed month retained: %+v", g)
			}
			if failure == "CSV" && s.Groups[1].BillingStatus != "complete" {
				t.Fatal("bad row incorrectly failed other owned group")
			}
		})
	}
}

func TestBillingNoRecords(t *testing.T) {
	for _, status := range []int{200, 204} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			s := billingSnapshot()
			client := &http.Client{Transport: billingTransport(func(*http.Request) (*http.Response, error) { return billingResponse(status, billingManifest()), nil })}
			Collect(context.Background(), client, billingCredential{}, s)
			if s.Groups[0].BillingStatus != "complete" || !billingHasDiagnostic(s, "billing-no-data", "error") || !billingHasDiagnostic(s, "billing-no-records", "info") {
				t.Fatalf("empty report treated as confirmed zero: %+v", s)
			}
		})
	}
}

func TestBillingOwnership(t *testing.T) {
	for _, mode := range []string{"missing", "invalid", "duplicate", "discovery conflict"} {
		t.Run(mode, func(t *testing.T) {
			s := billingSnapshot()
			switch mode {
			case "missing":
				s.Groups[0].SubscriptionID = ""
			case "invalid":
				s.Groups[0].SubscriptionID = "subscription-name"
			case "duplicate":
				s.Groups = append(s.Groups, Group{SubscriptionID: billingTestSub, Name: "OWNED", Owner: "another"})
			case "discovery conflict":
				s.Diagnose("error", "ownership-conflict", "owned", "conflicting claims")
			}
			client := &http.Client{Transport: billingTransport(func(*http.Request) (*http.Response, error) { t.Fatal("queried ambiguous ownership"); return nil, nil })}
			Collect(context.Background(), client, billingCredential{}, s)
			for _, g := range s.Groups {
				if g.BillingStatus != "unavailable" || len(g.Resources) != 0 {
					t.Fatalf("ambiguous group billed: %+v", g)
				}
			}
			if !s.HasErrors() {
				t.Fatal("missing ownership diagnostic")
			}
		})
	}
}

func TestBillingRetriesAndCancellation(t *testing.T) {
	for _, status := range []int{429, 503} {
		for _, recover := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/recover=%t", status, recover), func(t *testing.T) {
				attempts := 0
				client := &http.Client{Transport: billingTransport(func(*http.Request) (*http.Response, error) {
					attempts++
					if recover && attempts == 3 {
						return billingResponse(204, ""), nil
					}
					return billingResponse(status, "SECRET"), nil
				})}
				resp, err := billingRequest(context.Background(), client, billingCredential{}, "POST", "https://management.azure.com/report", "{}", true)
				if recover {
					if err != nil || attempts != 3 {
						t.Fatalf("retry failed: %d %v", attempts, err)
					}
					resp.Body.Close()
				} else if err == nil || attempts != 4 {
					t.Fatalf("retry not bounded: %d %v", attempts, err)
				}
			})
		}
	}
	for _, status := range []int{202, 429, 503} {
		t.Run(fmt.Sprintf("cancel-%d", status), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			calls := 0
			client := &http.Client{Transport: billingTransport(func(*http.Request) (*http.Response, error) {
				calls++
				resp := billingResponse(status, "")
				resp.Header.Set("Location", "https://management.azure.com/poll")
				resp.Header.Set("Retry-After", "120")
				return resp, nil
			})}
			s := billingSnapshot()
			Collect(ctx, client, billingCredential{}, s)
			if calls != 1 || s.Groups[0].BillingStatus != "unavailable" || !s.HasErrors() {
				t.Fatalf("Retry-After or cancellation ignored: calls=%d %+v", calls, s)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := billingWait(ctx, time.Now().Add(time.Hour).UTC().Format(http.TimeFormat), 0); err == nil {
		t.Fatal("HTTP-date Retry-After ignored cancellation")
	}
}

func TestBillingEndpointAndSecretSafety(t *testing.T) {
	for _, location := range []string{"http://management.azure.com/poll?sig=SECRET", "https://management.azure.com.evil.test/poll?sig=SECRET", "https://evil.test/poll?sig=SECRET", "https://user:SECRET@management.azure.com/poll", "https://management.azure.com:444/poll", "/poll?sig=SECRET"} {
		t.Run(location, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: billingTransport(func(*http.Request) (*http.Response, error) {
				calls++
				resp := billingResponse(202, "")
				resp.Header.Set("Location", location)
				return resp, nil
			})}
			s := billingSnapshot()
			Collect(context.Background(), client, billingCredential{}, s)
			if calls != 1 || !s.HasErrors() {
				t.Fatalf("untrusted poll followed: calls=%d", calls)
			}
			encoded, _ := json.Marshal(s)
			if strings.Contains(string(encoded), "SECRET") {
				t.Fatal("poll URL persisted in diagnostics")
			}
		})
	}
	for _, mode := range []string{"transport", "credential", "payload", "redirect", "blob redirect", "blob transport", "bad manifest", "blob host"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: billingTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				switch mode {
				case "transport":
					return nil, errors.New("https://management.azure.com/?sig=SECRET FAKE-TOKEN")
				case "payload":
					return billingResponse(403, `{"error":{"message":"SECRET FAKE-TOKEN"}}`), nil
				case "bad manifest":
					return billingResponse(200, `{"status":"Failed","error":{"message":"SECRET"}}`), nil
				case "blob host":
					return billingResponse(200, billingManifest("https://evil.test/?sig=SECRET")), nil
				}
				if mode == "redirect" || mode == "blob redirect" && calls == 2 {
					resp := billingResponse(302, "SECRET")
					resp.Header.Set("Location", "https://evil.test/?sig=SECRET")
					return resp, nil
				}
				if calls == 1 {
					return billingResponse(200, billingManifest(billingTestBlob)), nil
				}
				if r.Header.Get("Authorization") != "" {
					t.Fatal("blob bearer leaked")
				}
				return nil, errors.New("download failed: " + r.URL.String())
			})}
			cred := billingCredential{}
			if mode == "credential" {
				cred.err = errors.New("SECRET FAKE-TOKEN")
			}
			s := billingSnapshot()
			Collect(context.Background(), client, cred, s)
			encoded, _ := json.Marshal(s)
			if !s.HasErrors() || strings.Contains(string(encoded), "SECRET") || strings.Contains(string(encoded), "FAKE-TOKEN") || strings.Contains(string(encoded), "blob.core.windows.net") {
				t.Fatalf("unsafe error: %s", encoded)
			}
			if calls > 2 {
				t.Fatalf("redirect followed: %d", calls)
			}
		})
	}
}

func TestBillingDateWindows(t *testing.T) {
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		start, end string
		want       []billingWindow
	}{
		{"2026-09-18", "2026-09-18", []billingWindow{{"2026-09-18", "2026-09-18"}}},
		{"2026-01-31", "2026-03-01", []billingWindow{{"2026-01-31", "2026-01-31"}, {"2026-02-01", "2026-02-28"}, {"2026-03-01", "2026-03-01"}}},
		{"2025-12-31", "2026-01-01", []billingWindow{{"2025-12-31", "2025-12-31"}, {"2026-01-01", "2026-01-01"}}},
		{"2025-08-18", "2025-08-18", []billingWindow{{"2025-08-18", "2025-08-18"}}},
		{"2025-08-17", "2026-09-18", nil},
		{"2026-09-18", "2026-09-19", nil},
		{"2026-09-18", "2026-09-17", nil},
		{"bad", "2026-09-18", nil},
	} {
		t.Run(tc.start+"/"+tc.end, func(t *testing.T) {
			got, err := billingWindows(tc.start, tc.end, now)
			if !reflect.DeepEqual(got, tc.want) || (err != nil) != (tc.want == nil) {
				t.Fatalf("windows=%+v err=%v; want %+v", got, err, tc.want)
			}
		})
	}
	s := billingSnapshot()
	s.QueryStart = "2020-01-01"
	client := &http.Client{Transport: billingTransport(func(*http.Request) (*http.Response, error) { t.Fatal("unsupported range queried"); return nil, nil })}
	Collect(context.Background(), client, billingCredential{}, s)
	if s.QueryStart != "2020-01-01" || !billingHasDiagnostic(s, "billing-date-range", "error") || s.Groups[0].BillingStatus != "unavailable" {
		t.Fatalf("history silently truncated: %+v", s)
	}
	for _, tc := range []struct {
		now, start, end string
		want            []billingWindow
	}{
		{"2025-03-31", "2024-02-29", "2024-03-01", []billingWindow{{"2024-02-29", "2024-02-29"}, {"2024-03-01", "2024-03-01"}}},
		{"2026-03-31", "2025-02-28", "2025-02-28", []billingWindow{{"2025-02-28", "2025-02-28"}}},
	} {
		now, _ := time.Parse(time.DateOnly, tc.now)
		got, err := billingWindows(tc.start, tc.end, now)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("month-end history or leap boundary incorrect: %+v %v", got, err)
		}
	}
}

func TestBillingBlobRetry(t *testing.T) {
	s := billingSnapshot()
	blobCalls := 0
	client := &http.Client{Transport: billingTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "management.azure.com" {
			return billingResponse(200, billingManifest(billingTestBlob)), nil
		}
		if r.Header.Get("Authorization") != "" {
			t.Fatal("blob retry leaked ARM credentials")
		}
		blobCalls++
		if blobCalls < 3 {
			return billingResponse(503, "SECRET"), nil
		}
		return billingResponse(200, billingTestHeader+billingRow("owned", "", "2026-09-17", "1")), nil
	})}
	Collect(context.Background(), client, billingCredential{}, s)
	if s.HasErrors() || blobCalls != 3 || s.Groups[0].Resources[0].Charges[0].CostUSD != 1 {
		t.Fatalf("blob retry failed: %+v", s)
	}
}

func TestBillingMalformedReports(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"missing location", 202, ""},
		{"bad json", 200, "SECRET"},
		{"no manifest", 200, `{}`},
		{"incomplete blobs", 200, `{"status":"Completed","manifest":{"blobCount":1,"blobs":[]}}`},
		{"compressed", 200, `{"status":"Completed","manifest":{"compressData":true,"blobs":[]}}`},
		{"wrong format", 200, `{"status":"Completed","manifest":{"dataFormat":"Parquet","blobs":[]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := billingSnapshot()
			client := &http.Client{Transport: billingTransport(func(*http.Request) (*http.Response, error) {
				return billingResponse(tc.status, tc.body), nil
			})}
			Collect(context.Background(), client, billingCredential{}, s)
			if !s.HasErrors() || s.Groups[0].BillingStatus != "unavailable" {
				t.Fatalf("malformed report accepted: %+v", s)
			}
		})
	}
}

func TestBillingManagedCandidates(t *testing.T) {
	for _, tc := range []struct {
		name, category, kind, owner, suffix string
	}{
		{"double dash", "Tests", "customer", "test owner", "--managed"},
		{"single dash", "Tests", "customer", "test owner", "-managed"},
		{"service AKS", "Infra", "primary", "Service Cluster", "-aks1"},
		{"management AKS", "Infra", "primary", "Management Cluster 1", "-aks1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := billingSnapshot()
			parent := &s.Groups[0]
			parent.Category, parent.Kind, parent.Owner = tc.category, tc.kind, tc.owner
			parent.Attempts, parent.Outcomes = 2, []string{"failed", "passed"}
			s.Diagnose("error", "missing-managed-mapping", tc.owner, "2 cluster creations lack an exact managed resource group mapping; confirmed rejected PUTs excluded")
			data := billingTestHeader + billingRow("owned", "", "2026-09-17", "1") + billingRow(strings.ToUpper("owned"+tc.suffix), "", "2026-09-18", "0")
			Collect(context.Background(), billingCSVClient(t, data), billingCredential{}, s)
			if len(s.Groups) != 2 {
				t.Fatalf("candidate not confirmed: %+v", s.Groups)
			}
			g := s.Groups[1]
			if g.Name != "owned"+tc.suffix || g.Owner != tc.owner || g.Category != tc.category || g.SubscriptionID != billingTestSub || g.Attempts != 2 || !reflect.DeepEqual(g.Outcomes, []string{"failed", "passed"}) || g.BillingStatus != "complete" {
				t.Fatalf("candidate lost metadata: %+v", g)
			}
			wantKind := "hcp-managed"
			if tc.category == "Infra" {
				wantKind = "aks-managed"
			}
			if g.Kind != wantKind || g.Attribution != "Inferred from owned using the "+tc.suffix+" naming convention; confirmed in Azure billing" || len(g.Resources) != 1 || g.Resources[0].Charges[0].CostUSD != 0 {
				t.Fatalf("candidate has incorrect provenance or charges: %+v", g)
			}
			if !billingHasDiagnostic(s, "billing-inferred-attribution", "info") || !billingHasDiagnostic(s, "missing-managed-mapping", "error") {
				t.Fatalf("missing provenance or discovery error changed: %+v", s.Diagnostics)
			}
		})
	}
}

func TestBillingUnconfirmedCandidates(t *testing.T) {
	for _, mode := range []string{"absent", "outside dates", "wrong subscription", "prefix", "substring", "malformed", "failed request", "regional", "wrong kind", "missing subscription"} {
		t.Run(mode, func(t *testing.T) {
			s := billingSnapshot()
			s.Groups[0].Category, s.Groups[0].Kind = "Tests", "customer"
			candidate := billingRow("owned--managed", "", "2026-09-17", "1")
			switch mode {
			case "absent":
				candidate = ""
			case "outside dates":
				candidate = billingRow("owned--managed", "", "2026-09-16", "1")
			case "wrong subscription":
				candidate = strings.ReplaceAll(candidate, billingTestSub, "11111111-1111-1111-1111-111111111111")
			case "prefix":
				candidate = billingRow("owned--managed-extra", "", "2026-09-17", "1")
			case "substring":
				candidate = billingRow("extra-owned--managed", "", "2026-09-17", "1")
			case "malformed":
				candidate = billingRow("owned--managed", "", "2026-09-17", "NaN")
			case "regional":
				s.Groups[0].Category, s.Groups[0].Kind, s.Groups[0].Owner = "Infra", "primary", "Regional"
				candidate = billingRow("owned-aks1", "", "2026-09-17", "1")
			case "wrong kind":
				s.Groups[0].Kind = "hcp-managed"
			case "missing subscription":
				s.Groups[0].SubscriptionID = ""
			}
			client := billingCSVClient(t, billingTestHeader+billingRow("owned", "", "2026-09-17", "1")+candidate)
			if mode == "failed request" {
				client.Transport = billingTransport(func(*http.Request) (*http.Response, error) { return billingResponse(403, ""), nil })
			}
			Collect(context.Background(), client, billingCredential{}, s)
			if len(s.Groups) != 1 || billingHasDiagnostic(s, "billing-inferred-attribution", "info") {
				t.Fatalf("phantom inferred group: %+v", s)
			}
			matchedErrors := 0
			for _, d := range s.Diagnostics {
				if mode == "malformed" && d.Scope == billingTestSub+"/owned--managed" && d.Code == "billing-window" && d.Severity == "error" {
					matchedErrors++
					continue
				}
				if strings.Contains(d.Scope, "managed") || strings.Contains(d.Scope, "aks1") {
					t.Fatalf("phantom candidate diagnostic: %+v", d)
				}
			}
			if mode == "malformed" && (matchedErrors != 1 || !s.HasErrors()) {
				t.Fatalf("matched malformed candidate error lost: %+v", s.Diagnostics)
			}
		})
	}
}

func TestBillingMissingSubscriptionDiagnostic(t *testing.T) {
	for _, code := range []string{"missing-infra-subscription", "missing-test-subscription", "unrelated", ""} {
		for _, scope := range []string{"OWNED", "other"} {
			t.Run(code+"/"+scope, func(t *testing.T) {
				s := billingSnapshot()
				s.Groups[0].SubscriptionID = ""
				if code != "" {
					s.Diagnose("error", code, scope, "Missing artifact subscription")
				}
				client := &http.Client{Transport: billingTransport(func(*http.Request) (*http.Response, error) { t.Fatal("queried unknown subscription"); return nil, nil })}
				Collect(context.Background(), client, billingCredential{}, s)
				want := scope != "OWNED" || code != "missing-infra-subscription" && code != "missing-test-subscription"
				if billingHasDiagnostic(s, "billing-discovery-missing", "error") != want || s.Groups[0].BillingStatus != "unavailable" {
					t.Fatalf("incorrect missing-subscription diagnostic suppression: %+v", s)
				}
			})
		}
	}
}

func TestBillingMalformedCandidateBeforeBlobFailure(t *testing.T) {
	s := billingSnapshot()
	s.Groups[0].Category, s.Groups[0].Kind = "Tests", "customer"
	blobs := 0
	client := &http.Client{Transport: billingTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "management.azure.com" {
			return billingResponse(200, billingManifest(billingTestBlob, billingTestBlob+"2")), nil
		}
		blobs++
		if blobs == 2 {
			return billingResponse(403, ""), nil
		}
		return billingResponse(200, billingTestHeader+billingRow("owned--managed", "", "2026-09-17", "NaN")), nil
	})}
	Collect(context.Background(), client, billingCredential{}, s)
	if len(s.Groups) != 1 || !s.HasErrors() {
		t.Fatalf("malformed candidate promoted or error lost: %+v", s)
	}
	matchedErrors := 0
	for _, d := range s.Diagnostics {
		if d.Scope == billingTestSub+"/owned--managed" {
			if d.Code != "billing-window" || d.Severity != "error" || !strings.Contains(d.Message, "finite") {
				t.Fatalf("matched row error overwritten by global failure: %+v", d)
			}
			matchedErrors++
		}
		if d.Scope == billingTestSub+"/owned-managed" {
			t.Fatalf("unseen hypothetical candidate error emitted: %+v", d)
		}
	}
	if matchedErrors != 1 {
		t.Fatalf("matched row error missing: %+v", s.Diagnostics)
	}
}

func TestBillingExcludedGroups(t *testing.T) {
	for _, suffix := range []string{"--managed", "-managed", "-aks1"} {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/explicit=%t", suffix, explicit), func(t *testing.T) {
				s := billingSnapshot()
				s.Groups[0].Category, s.Groups[0].Kind = "Tests", "customer"
				if suffix == "-aks1" {
					s.Groups[0].Category, s.Groups[0].Kind, s.Groups[0].Owner = "Infra", "primary", "Service Cluster"
				}
				name := "owned" + suffix
				s.ExcludedGroups = []string{strings.ToUpper(name)}
				if explicit {
					s.Groups = append(s.Groups, Group{SubscriptionID: billingTestSub, Name: name, Category: "Tests", Kind: "hcp-managed", Owner: "test"})
				}
				data := billingTestHeader + billingRow("owned", "", "2026-09-17", "1") + billingRow(name, "", "2026-09-17", "999")
				Collect(context.Background(), billingCSVClient(t, data), billingCredential{}, s)
				if s.Groups[0].BillingStatus != "complete" || len(s.Groups[0].Resources) != 1 || s.Groups[0].Resources[0].Charges[0].CostUSD != 1 {
					t.Fatalf("valid parent lost: %+v", s)
				}
				if explicit {
					if len(s.Groups) != 2 || s.Groups[1].BillingStatus != "unavailable" || len(s.Groups[1].Resources) != 0 || !billingHasDiagnostic(s, "billing-excluded-group", "error") {
						t.Fatalf("excluded explicit group attributed: %+v", s)
					}
				} else if len(s.Groups) != 1 || s.HasErrors() {
					t.Fatalf("excluded candidate materialized: %+v", s)
				}
				if billingHasDiagnostic(s, "billing-inferred-attribution", "info") {
					t.Fatal("excluded candidate confirmed")
				}
			})
		}
	}
	t.Run("excluded parent", func(t *testing.T) {
		s := billingSnapshot()
		s.Groups[0].Category, s.Groups[0].Kind = "Tests", "customer"
		s.ExcludedGroups = []string{"OWNED"}
		client := &http.Client{Transport: billingTransport(func(*http.Request) (*http.Response, error) {
			t.Fatal("excluded parent generated billing query or candidates")
			return nil, nil
		})}
		Collect(context.Background(), client, billingCredential{}, s)
		if len(s.Groups) != 1 || s.Groups[0].BillingStatus != "unavailable" || !billingHasDiagnostic(s, "billing-excluded-group", "error") {
			t.Fatalf("excluded parent allowed: %+v", s)
		}
	})
}

func TestBillingCandidateOwnershipPrecedence(t *testing.T) {
	for _, mode := range []string{"explicit", "explicit conflict", "candidate conflict", "same owner", "parent conflict"} {
		t.Run(mode, func(t *testing.T) {
			s := billingSnapshot()
			s.Groups[0].Category, s.Groups[0].Kind, s.Groups[0].Owner = "Tests", "customer", "first"
			switch mode {
			case "explicit", "explicit conflict":
				s.Groups = append(s.Groups, Group{SubscriptionID: billingTestSub, Name: "OWNED--managed", Category: "Tests", Kind: "hcp-managed", Owner: "explicit"})
				if mode == "explicit conflict" {
					s.Diagnose("error", "ownership-conflict", "OWNED--managed", "conflict")
				}
			case "candidate conflict", "same owner":
				owner := "second"
				if mode == "same owner" {
					owner = "first"
				}
				s.Groups = append(s.Groups, Group{SubscriptionID: billingTestSub, Name: "OWNED-", Category: "Tests", Kind: "customer", Owner: owner})
			case "parent conflict":
				s.Diagnose("error", "ownership-conflict", "owned", "conflict")
			}
			Collect(context.Background(), billingCSVClient(t, billingTestHeader+billingRow("owned--managed", "", "2026-09-17", "1")), billingCredential{}, s)
			charged, inferred := 0, 0
			for _, g := range s.Groups {
				charged += len(g.Resources)
				if g.Attribution != "" {
					inferred++
				}
			}
			switch mode {
			case "explicit":
				if len(s.Groups) != 2 || charged != 1 || inferred != 0 || s.Groups[1].Owner != "explicit" {
					t.Fatalf("explicit discovery did not take precedence: %+v", s)
				}
			case "same owner":
				if len(s.Groups) != 3 || charged != 1 || inferred != 1 {
					t.Fatalf("same-owner candidate counted twice: %+v", s)
				}
			default:
				if charged != 0 || inferred != 0 || !s.HasErrors() {
					t.Fatalf("ambiguous ownership attributed: %+v", s)
				}
				if mode == "candidate conflict" && !billingHasDiagnostic(s, "billing-heuristic-conflict", "error") {
					t.Fatalf("candidate conflict not diagnosed: %+v", s.Diagnostics)
				}
			}
		})
	}
}

func TestBillingCandidatesAcrossWindows(t *testing.T) {
	for _, firstFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("first-fails=%t", firstFails), func(t *testing.T) {
			s := billingSnapshot()
			s.QueryStart, s.QueryEnd = "2026-08-31", "2026-09-01"
			s.Groups[0].Category, s.Groups[0].Kind = "Tests", "customer"
			month := 0
			client := &http.Client{Transport: billingTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "management.azure.com" {
					month++
					if (month == 1) == firstFails {
						return billingResponse(403, ""), nil
					}
					return billingResponse(200, billingManifest(billingTestBlob)), nil
				}
				return billingResponse(200, billingTestHeader+billingRow("owned--managed", "", "2026-08-31", "1")+billingRow("owned--managed", "", "2026-09-01", "2")), nil
			})}
			Collect(context.Background(), client, billingCredential{}, s)
			if len(s.Groups) != 2 || s.Groups[1].BillingStatus != "partial" || len(s.Groups[1].Resources[0].Charges) != 1 {
				t.Fatalf("candidate window status incorrect: %+v", s)
			}
			want := 1.0
			if firstFails {
				want = 2
			}
			if s.Groups[1].Resources[0].Charges[0].CostUSD != want {
				t.Fatalf("successful candidate window lost: %+v", s.Groups[1])
			}
			diagnostics := 0
			for _, d := range s.Diagnostics {
				if d.Code == "billing-window" && d.Scope == billingTestSub+"/owned--managed" {
					diagnostics++
				}
				if strings.Contains(d.Scope, "/owned-managed") {
					t.Fatalf("absent sibling diagnostic retained: %+v", d)
				}
			}
			if diagnostics != 1 {
				t.Fatalf("candidate failed window diagnostic lost: %+v", s.Diagnostics)
			}
		})
	}
}

func TestBillingCandidatesSubscriptionIsolation(t *testing.T) {
	s := billingSnapshot()
	s.Groups[0].Category, s.Groups[0].Kind, s.Groups[0].Owner = "Tests", "customer", "first"
	otherSub := "11111111-1111-1111-1111-111111111111"
	s.Groups = append(s.Groups, Group{SubscriptionID: otherSub, Name: "OWNED", Category: "Tests", Kind: "customer", Owner: "second"})
	queried := map[string]bool{}
	client := &http.Client{Transport: billingTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "management.azure.com" {
			queried[strings.Split(r.URL.Path, "/")[2]] = true
			return billingResponse(200, billingManifest(billingTestBlob)), nil
		}
		return billingResponse(200, billingTestHeader+billingRow("owned--managed", "", "2026-09-17", "1")), nil
	})}
	Collect(context.Background(), client, billingCredential{}, s)
	if len(queried) != 2 || len(s.Groups) != 3 || s.Groups[2].Owner != "first" || s.Groups[2].SubscriptionID != billingTestSub || billingHasDiagnostic(s, "billing-heuristic-conflict", "error") {
		t.Fatalf("candidate subscription isolation failed: %+v", s)
	}
}

func TestBillingReconcilesOnlyScopedSingleClusterGap(t *testing.T) {
	for _, mode := range []string{"matching", "other subscription", "other customer", "other owner", "multiple missing", "one missing of two", "unstructured", "both suffixes", "absent", "malformed", "explicit candidate", "parent conflict", "same-owner parent ambiguity", "infra"} {
		t.Run(mode, func(t *testing.T) {
			s := billingSnapshot()
			s.Groups[0].Category, s.Groups[0].Kind = "Tests", "customer"
			d := Diagnostic{Severity: "error", Code: "missing-managed-mapping", Scope: "test", SubscriptionID: strings.ToUpper(billingTestSub), ResourceGroup: "OWNED", ClusterCount: 1, MissingClusterCount: 1, Message: "Deliberately opaque; never parse this message"}
			candidate := billingRow("owned--managed", "", "2026-09-17", "0")
			switch mode {
			case "other subscription":
				d.SubscriptionID = "11111111-1111-1111-1111-111111111111"
			case "other customer":
				d.ResourceGroup = "another"
			case "other owner":
				d.Scope = "another test"
			case "multiple missing":
				d.ClusterCount, d.MissingClusterCount = 2, 2
			case "one missing of two":
				d.ClusterCount = 2
			case "unstructured":
				d.SubscriptionID, d.ResourceGroup = "", ""
				d.ClusterCount, d.MissingClusterCount = 0, 0
			case "both suffixes":
				candidate += billingRow("owned-managed", "", "2026-09-17", "1")
			case "absent":
				candidate = ""
			case "malformed":
				candidate = billingRow("owned--managed", "", "2026-09-17", "NaN")
			case "explicit candidate":
				s.Groups = append(s.Groups, Group{SubscriptionID: billingTestSub, Name: "owned--managed", Category: "Tests", Kind: "hcp-managed", Owner: "test"})
			case "parent conflict":
				s.Diagnose("error", "ownership-conflict", "owned", "conflicting test")
			case "same-owner parent ambiguity":
				s.Groups = append(s.Groups, Group{SubscriptionID: billingTestSub, Name: "owned-", Category: "Tests", Kind: "customer", Owner: "test"})
			case "infra":
				s.Groups[0].Category, s.Groups[0].Kind = "Infra", "primary"
				candidate = billingRow("owned-aks1", "", "2026-09-17", "1")
			}
			s.Diagnostics = append(s.Diagnostics, d)
			unrelated := Diagnostic{Severity: "error", Code: "missing-managed-mapping", Scope: "test", SubscriptionID: billingTestSub, ResourceGroup: "unrelated", ClusterCount: 1, MissingClusterCount: 1, Message: "Unrelated customer remains missing"}
			s.Diagnostics = append(s.Diagnostics, unrelated)
			Collect(context.Background(), billingCSVClient(t, billingTestHeader+billingRow("owned", "", "2026-09-17", "1")+candidate), billingCredential{}, s)
			resolved := billingHasDiagnostic(s, "billing-inferred-managed-mapping", "info")
			if resolved != (mode == "matching") {
				t.Fatalf("incorrect reconciliation for %s: %+v", mode, s.Diagnostics)
			}
			foundOriginal, foundUnrelated := false, false
			for _, got := range s.Diagnostics {
				if got == d {
					foundOriginal = true
				}
				if got == unrelated {
					foundUnrelated = true
				}
				if got.Code == "billing-inferred-managed-mapping" && (!strings.Contains(got.Message, "inferred") || got.ResourceGroup != "OWNED" || got.MissingClusterCount != 1) {
					t.Fatalf("lost inferred provenance: %+v", got)
				}
			}
			if !foundUnrelated || foundOriginal == (mode == "matching") {
				t.Fatalf("unrelated or unresolved error changed: %+v", s.Diagnostics)
			}
		})
	}
}

func TestBillingRecoveredSingleClusterNoLongerFailsSnapshot(t *testing.T) {
	s := billingSnapshot()
	s.Groups[0].Category, s.Groups[0].Kind = "Tests", "customer"
	s.Diagnostics = []Diagnostic{{Severity: "error", Code: "missing-managed-mapping", Scope: "test", SubscriptionID: billingTestSub, ResourceGroup: "owned", ClusterCount: 1, MissingClusterCount: 1, Message: "Exact mapping unavailable"}}
	// Diagnostics survive the same snapshot JSON boundary as CLI/offline reports.
	body, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatal(err)
	}
	Collect(context.Background(), billingCSVClient(t, billingTestHeader+billingRow("owned--managed", "", "2026-09-17", "1")), billingCredential{}, s)
	if s.HasErrors() || !billingHasDiagnostic(s, "billing-inferred-managed-mapping", "info") || len(s.Groups) != 2 || s.Groups[1].Attribution == "" {
		t.Fatalf("recovered single-cluster mapping still fails or lacks provenance: %+v", s)
	}
}
