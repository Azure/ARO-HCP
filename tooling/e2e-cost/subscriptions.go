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
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// ResolveSubscriptionNames adds optional ARM metadata only for explicit snapshot
// subscriptions. Lookup failures leave UUID-only labels and do not affect billing.
func ResolveSubscriptionNames(ctx context.Context, client *http.Client, credential azcore.TokenCredential, snapshot *Snapshot) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if client == nil {
		client = http.DefaultClient
	}
	httpClient := *client
	httpClient.Jar = nil
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	names := map[string]string{}
	for i := range snapshot.Subscriptions {
		sub := &snapshot.Subscriptions[i]
		id := strings.ToLower(sub.ID)
		sub.DisplayName = ""
		if !discoveryUUID.MatchString(id) {
			snapshot.Diagnose("info", "subscription-name-unavailable", "subscriptions", "Subscription name lookup skipped: an explicit subscription UUID is required")
			continue
		}
		if name, seen := names[id]; seen {
			sub.DisplayName = name
			continue
		}
		names[id] = ""
		message := "Subscription name lookup unavailable; using UUID (request or response details omitted)"
		if credential != nil {
			target := "https://management.azure.com/subscriptions/" + id + "?api-version=2022-12-01"
			resp, err := billingRequest(ctx, &httpClient, credential, http.MethodGet, target, "", true)
			if err == nil {
				var metadata struct {
					SubscriptionID string `json:"subscriptionId"`
					DisplayName    string `json:"displayName"`
				}
				if resp.StatusCode == http.StatusOK {
					err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&metadata)
					if err == nil && strings.EqualFold(metadata.SubscriptionID, id) && strings.TrimSpace(metadata.DisplayName) != "" {
						sub.DisplayName = metadata.DisplayName
						names[id] = sub.DisplayName
					} else {
						message = "Subscription name lookup returned invalid or conflicting metadata; using UUID (response details omitted)"
					}
				} else {
					message = fmt.Sprintf("Subscription name lookup HTTP %d; using UUID (response details omitted)", resp.StatusCode)
				}
				resp.Body.Close()
			}
		}
		if sub.DisplayName == "" {
			snapshot.Diagnose("info", "subscription-name-unavailable", id, message)
		}
	}
}

var (
	subscriptionInfraGroup = regexp.MustCompile(`(?i)^hcp-underlay-ci0[01]-j[0-9]{7}(-(svc|mgmt-[0-9]+)(-aks1)?)?$`)
	// The framework uses Kubernetes' consonant/digit alphabet for its random
	// 12-character suffix. Managed names can insert a version/role and random6.
	subscriptionTestGroup = regexp.MustCompile(`(?i)^.+-[bcdfghjklmnpqrstvwxz2456789]{12}((-[a-z0-9]+)*-[bcdfghjklmnpqrstvwxz2456789]{6})?(-[0-9]+)*(-{1,2}managed(-[0-9]+)?)?$`)
	// SuffixName truncates to exactly 64 characters with an eight-digit hash.
	// Restrict this weaker signal to rg- names, not arbitrary hashed groups.
	subscriptionHashedGroup = regexp.MustCompile(`(?i)^rg-[a-z0-9-]{52}-[0-9a-f]{8}$`)
)

// DefaultExcludedGroup returns an auditable, inferred naming reason or "".
// Unknown/shared names, including the shared identity pool, are retained.
// CollectSubscriptions applies user keep rules, user excludes, then defaults.
func DefaultExcludedGroup(name string) string {
	if strings.HasPrefix(strings.ToLower(name), "aro-hcp-msi-container-") {
		return ""
	}
	switch {
	case subscriptionInfraGroup.MatchString(name):
		return "Inferred CI infrastructure naming (ci00/ci01 and j plus seven digits)"
	case subscriptionTestGroup.MatchString(name):
		return "Inferred E2E random12 naming, including managed variants"
	case subscriptionHashedGroup.MatchString(name):
		return "Inferred E2E truncated rg- naming with eight-digit hash"
	default:
		return ""
	}
}

// CollectSubscriptions collects amortized USD billing for explicit subscriptions,
// without Prow discovery, tenant selection, inventory queries or Kusto. Dates
// are inclusive UTC days; omitting both selects today-3 through today-2. Regexes
// use Go syntax and match normalized lowercase resource-group names. Patterns
// retain their supplied case sensitivity (use lowercase patterns or (?i)).
// Keep rules override every exclusion. All errors are persisted in the snapshot;
// successfully parsed, scoped rows survive failures in other rows/blobs/months.
func CollectSubscriptions(ctx context.Context, client *http.Client, credential azcore.TokenCredential, subscriptionIDs []string, start, end string, excludePatterns, keepPatterns []string, now time.Time) *Snapshot {
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	if start == "" && end == "" {
		today := now.Truncate(24 * time.Hour)
		start, end = today.AddDate(0, 0, -3).Format(time.DateOnly), today.AddDate(0, 0, -2).Format(time.DateOnly)
	}
	s := &Snapshot{Version: SchemaVersion, Mode: "subscriptions", CollectedAt: now, Currency: "USD", CostBasis: "AmortizedCost", QueryStart: start, QueryEnd: end}
	s.Diagnose("info", "billing-provisional", "billing", "Azure billing is provisional: recent usage may arrive late and charges may be adjusted; complete means all requested intervals were retrieved, not that billing is final")
	windows, err := billingWindows(start, end, now)
	if err != nil {
		s.Diagnose("error", "billing-date-range", "billing", err.Error())
	}
	seen := map[string]bool{}
	for _, id := range subscriptionIDs {
		id = strings.ToLower(strings.TrimSpace(id))
		if !discoveryUUID.MatchString(id) {
			s.Diagnose("error", "billing-subscription", "billing", "Every subscription must be an explicit UUID")
			continue
		}
		if !seen[id] {
			seen[id] = true
			s.Subscriptions = append(s.Subscriptions, SubscriptionSummary{ID: id, BillingStatus: "unavailable"})
		}
	}
	if len(subscriptionIDs) == 0 {
		s.Diagnose("error", "billing-subscription", "billing", "At least one explicit subscription UUID is required")
	}
	var excludes, keeps []*regexp.Regexp
	for _, rules := range []struct {
		name     string
		patterns []string
		compiled *[]*regexp.Regexp
	}{{"exclude", excludePatterns, &excludes}, {"keep", keepPatterns, &keeps}} {
		for i, pattern := range rules.patterns {
			re, err := regexp.Compile(pattern)
			if err != nil {
				s.Diagnose("error", "billing-filter", rules.name, fmt.Sprintf("Invalid %s resource-group regex at position %d: %v", rules.name, i+1, err))
				continue
			}
			*rules.compiled = append(*rules.compiled, re)
		}
	}
	if credential == nil {
		s.Diagnose("error", "billing-credential", "billing", "Azure token credential is required")
	}
	if s.HasErrors() {
		return s // Validate the entire request before any network or authentication.
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	httpClient := *client
	httpClient.Jar = nil
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for si := range s.Subscriptions {
		summary := &s.Subscriptions[si]
		indices := map[string]int{}
		var groups []Group
		var reasons []string
		var costs []float64
		matched := map[int]bool{}
		selectGroup := func(name string) (int, bool) {
			key := strings.ToLower(name)
			if i, ok := indices[key]; ok {
				return i, true
			}
			reason := DefaultExcludedGroup(key)
			for _, re := range excludes {
				if re.MatchString(key) {
					reason = "User exclude regex: " + re.String()
					break
				}
			}
			for _, re := range keeps {
				if re.MatchString(key) {
					reason = ""
					break
				}
			}
			// Only the empty CSV value is absent. Literal "null", "notapplicable"
			// and other names remain distinct rather than silently merging costs.
			kind := "retained"
			if name == "" {
				name = "(No resource group)"
				kind = "unassigned"
			}
			i := len(groups)
			indices[key] = i
			groups = append(groups, Group{SubscriptionID: summary.ID, Name: name, Category: "Residual", Owner: summary.ID, Kind: kind, Attribution: "Retained by resource-group naming filters; not proof of non-E2E ownership"})
			reasons, costs = append(reasons, reason), append(costs, 0)
			return i, true
		}
		successes, failures, anyRecords := 0, false, false
		for _, window := range windows {
			rows, bad := map[int]map[billingKey]float64{}, map[int]string{}
			windowCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
			err := consumeBillingWindowBlobs(windowCtx, &httpClient, credential, summary.ID, window, func(body io.Reader) error {
				return readBillingCSVGroups(windowCtx, body, summary.ID, window, selectGroup, true, rows, bad)
			}, true)
			cancel()
			if err != nil {
				failures = true
				s.Diagnose("error", "billing-window", summary.ID, window.Start+" through "+window.End+": "+err.Error())
			}
			// Keep deterministic output while retaining valid rows even if another
			// row or later blob failed. Job mode intentionally keeps its old policy.
			for i := range groups {
				keys := make([]billingKey, 0, len(rows[i]))
				for key := range rows[i] {
					keys = append(keys, key)
				}
				sort.Slice(keys, func(a, b int) bool { return fmt.Sprint(keys[a]) < fmt.Sprint(keys[b]) })
				for _, key := range keys {
					amount := rows[i][key]
					total := costs[i] + amount
					retained, excluded := summary.RetainedUSD, summary.ExcludedUSD
					if reasons[i] == "" {
						retained += amount
					} else {
						excluded += amount
					}
					if math.IsInf(total, 0) || math.IsInf(retained, 0) || math.IsInf(excluded, 0) || math.IsInf(retained+excluded, 0) {
						bad[i] = "Billing aggregate exceeds finite USD range; overflowing charge excluded"
						continue
					}
					costs[i], summary.RetainedUSD, summary.ExcludedUSD = total, retained, excluded
					matched[i], anyRecords = true, true
					if reasons[i] == "" {
						addBillingCharge(&groups[i], key, amount)
					}
				}
			}
			if err == nil && len(bad) == 0 {
				successes++
			}
			for i := -1; i < len(groups); i++ {
				if message := bad[i]; message != "" {
					failures = true
					scope := summary.ID
					if i >= 0 {
						scope += "/" + groups[i].Name
					}
					s.Diagnose("error", "billing-window", scope, window.Start+" through "+window.End+": "+message)
				}
			}
		}
		summary.TotalUSD = summary.RetainedUSD + summary.ExcludedUSD
		if successes > 0 || anyRecords {
			summary.BillingStatus = "complete"
			if failures {
				summary.BillingStatus = "partial"
			}
		}
		if !failures && !anyRecords {
			s.Diagnose("info", "billing-no-records", summary.ID, "No billing records in successfully retrieved intervals; reported cost is zero and remains provisional")
		}
		for i, g := range groups {
			if !matched[i] {
				continue
			}
			if reasons[i] != "" {
				s.FilteredGroups = append(s.FilteredGroups, FilteredGroup{SubscriptionID: summary.ID, Name: g.Name, Reason: reasons[i], CostUSD: costs[i]})
			} else {
				g.BillingStatus = summary.BillingStatus
				s.Groups = append(s.Groups, g)
			}
		}
	}
	return s
}
