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
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type billingWindow struct{ Start, End string }

type billingKey struct {
	ID, Name, Type, Day, MeterID, MeterName, MeterCategory string
}

// Collect adds Azure-reported amortized USD charges to artifact-owned groups.
// QueryStart and QueryEnd are inclusive UTC dates. Subscription-wide reports
// are streamed and only owned groups' aggregates are retained.
func Collect(ctx context.Context, client *http.Client, credential azcore.TokenCredential, snapshot *Snapshot) {
	snapshot.Currency, snapshot.CostBasis = "USD", "AmortizedCost"
	snapshot.Diagnose("info", "billing-provisional", "billing", "Azure billing is provisional: recent usage may arrive late and charges may be adjusted; complete means all requested intervals were retrieved, not that billing is final")
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	// Never let redirects forward credentials or a report SAS to another host.
	httpClient := *client
	httpClient.Jar = nil
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	owned := map[string]map[string]int{}
	claims := map[string][]int{}
	excluded := map[string]bool{}
	for _, name := range snapshot.ExcludedGroups {
		excluded[strings.ToLower(name)] = true
	}
	for i := range snapshot.Groups {
		g := &snapshot.Groups[i]
		if excluded[strings.ToLower(g.Name)] {
			g.BillingStatus = "unavailable"
			snapshot.Diagnose("error", "billing-excluded-group", g.Name, "Discovered ownership conflicts with the shared resource group deny list; billing attribution disabled")
			continue
		}
		if !discoveryUUID.MatchString(g.SubscriptionID) {
			g.BillingStatus = "unavailable"
			alreadyDiagnosed := false
			for _, d := range snapshot.Diagnostics {
				if (d.Code == "missing-infra-subscription" || d.Code == "missing-test-subscription") && strings.EqualFold(d.Scope, g.Name) {
					alreadyDiagnosed = true
					break
				}
			}
			if !alreadyDiagnosed {
				snapshot.Diagnose("error", "billing-discovery-missing", g.Name, "Artifact subscription is missing or invalid; billing skipped without inferring a subscription")
			}
			continue
		}
		key := strings.ToLower(g.SubscriptionID + "/" + g.Name)
		claims[key] = append(claims[key], i)
	}
	for _, indices := range claims {
		for _, i := range indices {
			g := &snapshot.Groups[i]
			conflict := len(indices) > 1
			for _, d := range snapshot.Diagnostics {
				if d.Code == "ownership-conflict" && strings.EqualFold(d.Scope, g.Name) {
					conflict = true
				}
			}
			if conflict {
				g.BillingStatus = "unavailable"
				snapshot.Diagnose("error", "billing-ownership-conflict", g.Name, "Multiple ownership claims; billing skipped to prevent double attribution")
				continue
			}
			sub := strings.ToLower(g.SubscriptionID)
			if owned[sub] == nil {
				owned[sub] = map[string]int{}
			}
			owned[sub][strings.ToLower(g.Name)] = i
			g.BillingStatus = "unavailable"
		}
	}
	now := snapshot.CollectedAt
	if now.IsZero() {
		now = time.Now()
	}
	windows, err := billingWindows(snapshot.QueryStart, snapshot.QueryEnd, now)
	if err != nil {
		snapshot.Diagnose("error", "billing-date-range", "billing", err.Error())
		return
	}
	if credential == nil {
		snapshot.Diagnose("error", "billing-credential", "billing", "Azure token credential is required")
		return
	}
	// Candidates remain local until a successfully parsed billing window
	// confirms an exact (subscription, resource-group) match.
	originalCount := len(snapshot.Groups)
	groups := append([]Group(nil), snapshot.Groups...)
	candidates := map[string]int{}
	candidateParents := map[int]int{}
	ambiguous := map[int]bool{}
	for i, parent := range snapshot.Groups {
		sub := strings.ToLower(parent.SubscriptionID)
		if index, ok := owned[sub][strings.ToLower(parent.Name)]; !ok || index != i {
			continue
		}
		var suffixes []string
		kind := "hcp-managed"
		switch {
		case parent.Category == "Tests" && parent.Kind == "customer":
			suffixes = []string{"--managed", "-managed"}
		case parent.Category == "Infra" && parent.Kind == "primary" && parent.Owner != "Regional":
			suffixes, kind = []string{"-aks1"}, "aks-managed"
		}
		for _, suffix := range suffixes {
			name := parent.Name + suffix
			if excluded[strings.ToLower(name)] {
				continue
			}
			key := sub + "/" + strings.ToLower(name)
			if len(claims[key]) != 0 {
				continue // Explicit discovery, even a conflicting claim, takes precedence.
			}
			if previous, ok := candidates[key]; ok {
				// Even same-owner candidates can have two different customer parents.
				// Retain their costs, but do not use them to resolve a mapping gap.
				if candidateParents[previous] != i {
					candidateParents[previous] = -1
				}
				if groups[previous].Owner != parent.Owner || groups[previous].Category != parent.Category {
					if !ambiguous[previous] {
						snapshot.Diagnose("error", "billing-heuristic-conflict", key, "Managed resource group naming candidates have conflicting owners; billing attribution disabled")
					}
					ambiguous[previous] = true
					delete(owned[sub], strings.ToLower(name))
				}
				continue
			}
			index := len(groups)
			candidates[key] = index
			candidateParents[index] = i
			owned[sub][strings.ToLower(name)] = index
			groups = append(groups, Group{
				SubscriptionID: parent.SubscriptionID, Name: name, Category: parent.Category,
				Owner: parent.Owner, Kind: kind, Attempts: parent.Attempts,
				Outcomes: append([]string(nil), parent.Outcomes...), BillingStatus: "unavailable",
				Attribution: "Inferred from " + parent.Name + " using the " + suffix + " naming convention; confirmed in Azure billing",
			})
		}
	}
	successes, failures, records := make([]int, len(groups)), make([]bool, len(groups)), make([]bool, len(groups))
	candidateDiagnostics := map[int][]Diagnostic{}
	subs := make([]string, 0, len(owned))
	for sub := range owned {
		subs = append(subs, sub)
	}
	sort.Strings(subs)
	anySuccess, anyRecords := false, false
	for _, sub := range subs {
		for _, window := range windows {
			windowCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
			rows, bad, err := collectBillingWindow(windowCtx, &httpClient, credential, sub, owned[sub], window)
			cancel()
			for _, i := range owned[sub] {
				g := &groups[i]
				message := bad[i]
				if message == "" && err != nil {
					message = err.Error()
				}
				if message != "" {
					failures[i] = true
					diagnostic := Diagnostic{Severity: "error", Code: "billing-window", Scope: sub + "/" + g.Name, Message: window.Start + " through " + window.End + ": " + message}
					// A bad row establishes an actual candidate match, even when
					// it cannot confirm ownership or produce a usable charge.
					if i < originalCount || bad[i] != "" {
						snapshot.Diagnostics = append(snapshot.Diagnostics, diagnostic)
					} else {
						candidateDiagnostics[i] = append(candidateDiagnostics[i], diagnostic)
					}
					continue
				}
				successes[i]++
				anySuccess = true
				keys := make([]billingKey, 0, len(rows[i]))
				for key := range rows[i] {
					keys = append(keys, key)
				}
				sort.Slice(keys, func(a, b int) bool { return fmt.Sprint(keys[a]) < fmt.Sprint(keys[b]) })
				for _, key := range keys {
					addBillingCharge(g, key, rows[i][key])
					records[i], anyRecords = true, true
				}
			}
		}
	}
	confirmed := map[int][]int{}
	for candidate, parent := range candidateParents {
		if parent >= 0 && records[candidate] && !ambiguous[candidate] {
			confirmed[parent] = append(confirmed[parent], candidate)
		}
	}
	for parent, indices := range confirmed {
		if len(indices) != 1 {
			continue
		}
		p, g := groups[parent], groups[indices[0]]
		if p.Category != "Tests" || p.Kind != "customer" || g.Kind != "hcp-managed" {
			continue
		}
		for i := range snapshot.Diagnostics {
			d := &snapshot.Diagnostics[i]
			if d.Code != "missing-managed-mapping" || d.Severity != "error" || d.Scope != p.Owner ||
				d.ClusterCount != 1 || d.MissingClusterCount != 1 ||
				!strings.EqualFold(d.SubscriptionID, p.SubscriptionID) || !strings.EqualFold(d.ResourceGroup, p.Name) {
				continue
			}
			d.Severity = "info"
			d.Code = "billing-inferred-managed-mapping"
			d.Message = fmt.Sprintf("%s: exact artifact mapping unavailable for the single cluster; billing confirms naming candidate %s. Attribution is inferred, not an exact cluster mapping", p.Name, g.Name)
		}
	}
	snapshot.Groups = snapshot.Groups[:0]
	for i := range groups {
		g := &groups[i]
		if i >= originalCount {
			if !records[i] {
				continue
			}
			snapshot.Diagnostics = append(snapshot.Diagnostics, candidateDiagnostics[i]...)
			snapshot.Diagnose("info", "billing-inferred-attribution", g.SubscriptionID+"/"+g.Name, g.Attribution)
		}
		if successes[i] > 0 {
			g.BillingStatus = "complete"
			if failures[i] {
				g.BillingStatus = "partial"
			}
			if !records[i] {
				snapshot.Diagnose("info", "billing-no-records", g.SubscriptionID+"/"+g.Name, "No attributed billing records in successfully retrieved intervals; absence is not a confirmed zero charge")
			}
		}
		snapshot.Groups = append(snapshot.Groups, *g)
	}
	if anySuccess && !anyRecords {
		snapshot.Diagnose("error", "billing-no-data", "billing", "Queries succeeded but no billing rows were attributable to owned resource groups; billing may not yet be available")
	}
}

func billingWindows(start, end string, now time.Time) ([]billingWindow, error) {
	first, err1 := time.Parse(time.DateOnly, start)
	last, err2 := time.Parse(time.DateOnly, end)
	if err1 != nil || err2 != nil || first.After(last) {
		return nil, errors.New("expected an ordered, inclusive YYYY-MM-DD query range")
	}
	today := now.UTC().Truncate(24 * time.Hour)
	// AddDate normalizes invalid dates forward (March 31 minus 13 months can
	// land in March). Clamp to the last day of the target month instead.
	month := time.Date(today.Year(), today.Month()-13, 1, 0, 0, 0, 0, time.UTC)
	oldest := month.AddDate(0, 0, min(today.Day(), month.AddDate(0, 1, -1).Day())-1)
	if first.Before(oldest) || last.After(today) {
		return nil, errors.New("cost details supports only the past 13 months through today; the requested range was not truncated or queried")
	}
	var result []billingWindow
	for day := first; !day.After(last); {
		next := time.Date(day.Year(), day.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		end := next.AddDate(0, 0, -1)
		if end.After(last) {
			end = last
		}
		result = append(result, billingWindow{day.Format(time.DateOnly), end.Format(time.DateOnly)})
		day = next
	}
	return result, nil
}

func billingURL(raw string, authenticated bool) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if authenticated {
		return host == "management.azure.com"
	}
	return strings.HasSuffix(host, ".blob.core.windows.net")
}

func billingWait(ctx context.Context, header string, fallback time.Duration) error {
	delay := fallback
	if seconds, err := strconv.ParseUint(header, 10, 32); err == nil {
		delay = time.Duration(seconds) * time.Second
	} else if date, err := http.ParseTime(header); err == nil {
		delay = max(0, time.Until(date))
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return errors.New("billing request cancelled or timed out")
	case <-timer.C:
		return nil
	}
}

func billingRequest(ctx context.Context, client *http.Client, credential azcore.TokenCredential, method, target, body string, authenticated bool) (*http.Response, error) {
	if !billingURL(target, authenticated) {
		return nil, errors.New("rejected untrusted billing endpoint")
	}
	for attempt := 0; attempt < 4; attempt++ {
		if ctx.Err() != nil {
			return nil, errors.New("billing request cancelled or timed out")
		}
		req, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
		if err != nil {
			return nil, errors.New("invalid billing request")
		}
		if authenticated {
			token, err := credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{"https://management.azure.com/.default"}})
			if err != nil {
				return nil, errors.New("azure authentication failed; check Azure CLI login and billing access")
			}
			req.Header.Set("Authorization", "Bearer "+token.Token)
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			// http.Client errors embed URLs, including download SAS credentials.
			return nil, errors.New("billing HTTP request failed or was cancelled; endpoint and response details omitted")
		}
		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusServiceUnavailable {
			return resp, nil
		}
		resp.Body.Close()
		if attempt == 3 {
			return nil, fmt.Errorf("billing HTTP %d after four attempts", resp.StatusCode)
		}
		if err := billingWait(ctx, resp.Header.Get("Retry-After"), time.Second<<attempt); err != nil {
			return nil, err
		}
	}
	panic("unreachable")
}

func collectBillingWindow(ctx context.Context, client *http.Client, credential azcore.TokenCredential, sub string, owned map[string]int, window billingWindow) (map[int]map[billingKey]float64, map[int]string, error) {
	rows, bad := map[int]map[billingKey]float64{}, map[int]string{}
	err := consumeBillingWindow(ctx, client, credential, sub, window, func(body io.Reader) error {
		return readBillingCSV(ctx, body, sub, owned, window, rows, bad)
	})
	return rows, bad, err
}

// consumeBillingWindow shares report generation, polling and streaming downloads
// without coupling them to job ownership or subscription filtering.
func consumeBillingWindow(ctx context.Context, client *http.Client, credential azcore.TokenCredential, sub string, window billingWindow, consume func(io.Reader) error) error {
	return consumeBillingWindowBlobs(ctx, client, credential, sub, window, consume, false)
}

// Subscription reports retain independent blobs after a failure. Job reports
// keep their original fail-fast, whole-window attribution policy.
func consumeBillingWindowBlobs(ctx context.Context, client *http.Client, credential azcore.TokenCredential, sub string, window billingWindow, consume func(io.Reader) error, continueBlobs bool) error {
	body := fmt.Sprintf(`{"metric":"AmortizedCost","timePeriod":{"start":%q,"end":%q}}`, window.Start, window.End)
	target := "https://management.azure.com/subscriptions/" + sub + "/providers/Microsoft.CostManagement/generateCostDetailsReport?api-version=2025-03-01"
	method := http.MethodPost
	for poll := 0; poll < 180; poll++ {
		resp, err := billingRequest(ctx, client, credential, method, target, body, true)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusAccepted {
			resp.Body.Close()
			location := resp.Header.Get("Location")
			if location != "" {
				target = location
			} else if method == http.MethodPost {
				return errors.New("billing asynchronous response has no polling location")
			}
			if !billingURL(target, true) {
				return errors.New("rejected untrusted billing polling endpoint")
			}
			method, body = http.MethodGet, ""
			if err := billingWait(ctx, resp.Header.Get("Retry-After"), 5*time.Second); err != nil {
				return err
			}
			continue
		}
		if resp.StatusCode == http.StatusNoContent {
			resp.Body.Close()
			return nil
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return fmt.Errorf("billing HTTP %d; verify Cost Management access and EA/MCA subscription support (response details omitted)", resp.StatusCode)
		}
		var report struct {
			Status   string `json:"status"`
			Manifest *struct {
				BlobCount    *int   `json:"blobCount"`
				CompressData bool   `json:"compressData"`
				DataFormat   string `json:"dataFormat"`
				Blobs        []struct {
					BlobLink string `json:"blobLink"`
				} `json:"blobs"`
			} `json:"manifest"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&report)
		resp.Body.Close()
		if err != nil || report.Manifest == nil || (report.Status != "" && !strings.EqualFold(report.Status, "Completed")) {
			return errors.New("billing report failed or returned an invalid completion manifest (response details omitted)")
		}
		manifest := report.Manifest
		if manifest.CompressData || (manifest.DataFormat != "" && !strings.EqualFold(manifest.DataFormat, "Csv")) || (manifest.BlobCount != nil && *manifest.BlobCount != len(manifest.Blobs)) {
			return errors.New("unsupported billing report format or incomplete blob manifest")
		}
		var blobErrors []error
		for _, blob := range manifest.Blobs {
			resp, err := billingRequest(ctx, client, nil, http.MethodGet, blob.BlobLink, "", false)
			if err == nil {
				if resp.StatusCode != http.StatusOK {
					err = fmt.Errorf("billing blob download HTTP %d (response details omitted)", resp.StatusCode)
				} else {
					err = consume(resp.Body)
				}
				resp.Body.Close()
			}
			if err != nil {
				if !continueBlobs {
					return err
				}
				blobErrors = append(blobErrors, err)
				if ctx.Err() != nil {
					break
				}
			}
		}
		return errors.Join(blobErrors...)
	}
	return errors.New("billing report polling limit exceeded")
}

func readBillingCSV(ctx context.Context, body io.Reader, sub string, owned map[string]int, window billingWindow, rows map[int]map[billingKey]float64, bad map[int]string) error {
	return readBillingCSVGroups(ctx, body, sub, window, func(name string) (int, bool) {
		i, ok := owned[strings.ToLower(name)]
		return i, ok
	}, false, rows, bad)
}

// Subscription reports accept scoped purchases without ARM IDs. Job reports
// still require an exact resource-group/ARM-ID ownership match.
func readBillingCSVGroups(ctx context.Context, body io.Reader, sub string, window billingWindow, selectGroup func(string) (int, bool), subscriptionMode bool, rows map[int]map[billingKey]float64, bad map[int]string) error {
	r := csv.NewReader(body)
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return errors.New("missing or malformed billing CSV header")
	}
	columns := map[string]int{}
	for i, h := range header {
		h = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")), " ", ""))
		// MCA Cost Details reports use resourceGroupName rather than the
		// ResourceGroup column documented for the common cost-details schema.
		if h == "resourcegroupname" {
			h = "resourcegroup"
		}
		if _, exists := columns[h]; exists {
			return errors.New("duplicate billing CSV column")
		}
		columns[h] = i
	}
	if _, ok := columns["resourcegroup"]; !ok {
		return errors.New("billing CSV is missing ResourceGroup")
	}
	_, hasDate := columns["date"]
	_, hasUsageDate := columns["usagedatetime"]
	if !hasDate && !hasUsageDate {
		return errors.New("billing CSV is missing Date/UsageDateTime")
	}
	_, hasUSD := columns["costinusd"]
	_, hasCost := columns["cost"]
	_, hasBillingCost := columns["costinbillingcurrency"]
	_, hasCurrency := columns["currency"]
	_, hasBillingCurrency := columns["billingcurrency"]
	_, hasBillingCurrencyCode := columns["billingcurrencycode"]
	hasBillingAmount := (hasCost || hasBillingCost) && (hasCurrency || hasBillingCurrency || hasBillingCurrencyCode)
	if !hasUSD && !hasBillingAmount {
		return errors.New("billing CSV is missing supported USD cost/currency columns")
	}
	get := func(row []string, names ...string) string {
		for _, name := range names {
			if i, ok := columns[name]; ok && i < len(row) && strings.TrimSpace(row[i]) != "" {
				return strings.TrimSpace(row[i])
			}
		}
		return ""
	}
	for {
		if ctx.Err() != nil {
			return errors.New("billing CSV processing cancelled")
		}
		row, err := r.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return errors.New("malformed or unreadable billing CSV (row contents omitted)")
		}
		rowSub := get(row, "subscriptionid")
		if subscriptionMode && rowSub != "" && !strings.EqualFold(rowSub, sub) {
			bad[-1] = "Billing row subscription conflicts with the queried subscription; row excluded"
			continue
		}
		// Job ownership is selected first so unrelated malformed rows remain
		// ignored. Subscription classification waits until date/scope validation.
		i := -1
		if !subscriptionMode {
			var ok bool
			i, ok = selectGroup(get(row, "resourcegroup"))
			if !ok {
				continue
			}
		}
		if rowSub != "" && !strings.EqualFold(rowSub, sub) {
			continue
		}
		if len(row) != len(header) {
			bad[i] = "Attributed billing row has an incorrect field count"
			continue
		}
		day := get(row, "date", "usagedatetime")
		var date time.Time
		for _, layout := range []string{time.DateOnly, "01/02/2006", "1/2/2006", time.RFC3339} {
			date, err = time.Parse(layout, day)
			if err == nil {
				break
			}
		}
		if err != nil {
			bad[i] = "Attributed billing row has a missing or invalid Date/UsageDateTime"
			continue
		}
		day = date.UTC().Format(time.DateOnly)
		// Filter each inclusive interval, not just the overall range, to avoid
		// counting boundary rows twice if Azure returns overlapping reports.
		if day < window.Start || day > window.End {
			continue
		}
		id := get(row, "resourceid", "instanceid")
		name, resourceType := "Unspecified", get(row, "metercategory")
		groupConflict := false
		if id != "" {
			// Even malformed ARM IDs can carry an explicit subscription scope;
			// accepting non-ARM purchases must not bypass that boundary.
			if subscriptionMode && strings.HasPrefix(strings.ToLower(id), "/subscriptions/") {
				idSub := strings.SplitN(id[len("/subscriptions/"):], "/", 2)[0]
				if !strings.EqualFold(idSub, sub) {
					bad[i] = "Billing resource ID conflicts with the queried subscription; row excluded"
					continue
				}
			}
			parsed, err := azcorearm.ParseResourceID(id)
			if !subscriptionMode && (err != nil || !strings.EqualFold(parsed.SubscriptionID, sub) || !strings.EqualFold(parsed.ResourceGroupName, get(row, "resourcegroup"))) {
				bad[i] = "Attributed billing row has an invalid or conflicting ARM resource ID"
				continue
			}
			if err == nil {
				if parsed.SubscriptionID != "" && !strings.EqualFold(parsed.SubscriptionID, sub) {
					bad[i] = "Billing ARM resource ID conflicts with the queried subscription; row excluded"
					continue
				}
				if !strings.EqualFold(parsed.ResourceGroupName, get(row, "resourcegroup")) {
					groupConflict = true
				}
				id, name, resourceType = strings.ToLower(id), parsed.Name, parsed.ResourceType.String()
			} else {
				name = id
			}
		}
		if subscriptionMode {
			var ok bool
			i, ok = selectGroup(get(row, "resourcegroup"))
			if !ok {
				continue
			}
		}
		if groupConflict {
			bad[i] = "Billing resource group conflicts with the ARM resource ID; charge retained under the billing resource group"
		}
		if resourceType == "" {
			resourceType = "Unspecified"
			if subscriptionMode {
				resourceType = "Unknown"
			}
		}
		amount := get(row, "costinusd")
		if strings.EqualFold(get(row, "billingcurrency", "billingcurrencycode", "currency"), "USD") {
			amount = get(row, "costinbillingcurrency", "cost")
			if amount == "" {
				amount = get(row, "costinusd")
			}
		}
		value, err := strconv.ParseFloat(amount, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			bad[i] = "Attributed billing row has no valid finite Azure-reported USD cost; currency conversion is not performed"
			continue
		}
		key := billingKey{id, name, resourceType, day, get(row, "meterid"), get(row, "metername"), get(row, "metercategory")}
		if rows[i] == nil {
			rows[i] = map[billingKey]float64{}
		}
		sum := rows[i][key] + value
		if math.IsInf(sum, 0) || math.IsNaN(sum) {
			bad[i] = "Attributed billing aggregate exceeds finite USD range"
			continue
		}
		rows[i][key] = sum
	}
}

func addBillingCharge(g *Group, key billingKey, amount float64) {
	index := -1
	for i := range g.Resources {
		r := &g.Resources[i]
		if key.ID != "" && strings.EqualFold(r.ID, key.ID) || key.ID == "" && r.ID == "" && r.Type == key.Type {
			index = i
			break
		}
	}
	if index == -1 {
		g.Resources = append(g.Resources, Resource{ID: key.ID, Name: key.Name, Type: key.Type, Source: "billing"})
		index = len(g.Resources) - 1
	}
	r := &g.Resources[index]
	if r.Source == "artifact" {
		r.Source = "both"
	}
	if r.Type == "" {
		r.Type = key.Type
	}
	for i := range r.Charges {
		c := &r.Charges[i]
		if c.Day == key.Day && c.MeterID == key.MeterID && c.MeterName == key.MeterName && c.MeterCategory == key.MeterCategory {
			c.CostUSD += amount
			return
		}
	}
	r.Charges = append(r.Charges, Charge{Day: key.Day, MeterID: key.MeterID, MeterName: key.MeterName, MeterCategory: key.MeterCategory, CostUSD: amount})
}
