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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// Lookup inputs belong to the artifact discovery session, not the offline report.
type infraLookup struct {
	endpoint, database string
	bindings           []infraBinding
	invalid            bool
}

type infraBinding struct {
	rg, cluster, owner, nodeRG, regionalRG string
	conflict                               bool
}

var kustoDNSLabel = regexp.MustCompile(`(?i)^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var kustoAKSName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}$`)

func (d *discovery) infraLookupConfig(data any) {
	if d.snapshot.infraLookup == nil {
		d.snapshot.infraLookup = &infraLookup{}
	}
	l := d.snapshot.infraLookup
	k := discoveryMap(data, "kusto")
	name, location, database := discoveryString(k, "kustoName"), discoveryString(k, "location"), discoveryString(k, "serviceLogsDatabase")
	if !kustoDNSLabel.MatchString(name) || !kustoDNSLabel.MatchString(location) || strings.TrimSpace(database) == "" {
		l.invalid = true
		return
	}
	endpoint := "https://" + name + "." + location + ".kusto.windows.net"
	if l.endpoint != "" && (!strings.EqualFold(l.endpoint, endpoint) || l.database != database) {
		l.invalid = true
		return
	}
	l.endpoint, l.database = endpoint, database
}

func (d *discovery) infraBinding(binding infraBinding) {
	if !discoveryRG.MatchString(binding.rg) || discoveryRedacted.MatchString(binding.rg) || binding.cluster != "" && (!kustoAKSName.MatchString(binding.cluster) || discoveryRedacted.MatchString(binding.cluster)) {
		return // Missing exact AKS names must never be guessed from resource groups.
	}
	if d.snapshot.infraLookup == nil {
		d.snapshot.infraLookup = &infraLookup{}
	}
	l := d.snapshot.infraLookup
	for i := range l.bindings {
		old := &l.bindings[i]
		if !strings.EqualFold(old.rg, binding.rg) {
			continue
		}
		if (old.cluster != "" && binding.cluster != "" && !strings.EqualFold(old.cluster, binding.cluster)) || old.owner != binding.owner ||
			(old.nodeRG != "" && binding.nodeRG != "" && !strings.EqualFold(old.nodeRG, binding.nodeRG)) ||
			(old.regionalRG != "" && binding.regionalRG != "" && !strings.EqualFold(old.regionalRG, binding.regionalRG)) {
			old.conflict, binding.conflict = true, true
			d.snapshot.Diagnose("error", "infra-lookup-conflict", binding.rg, "Artifacts disagree on the exact AKS binding or its associated resource groups; subscription lookup disabled")
		}
		if old.cluster == "" || binding.cluster == "" || strings.EqualFold(old.cluster, binding.cluster) {
			if old.cluster == "" {
				old.cluster = binding.cluster
			}
			if old.nodeRG == "" {
				old.nodeRG = binding.nodeRG
			}
			if old.regionalRG == "" {
				old.regionalRG = binding.regionalRG
			}
			return
		}
	}
	l.bindings = append(l.bindings, binding)
}

func kustoEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Host != u.Hostname() || u.Path != "" || strings.ContainsAny(endpoint, "?#") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if !strings.HasSuffix(host, ".kusto.windows.net") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !kustoDNSLabel.MatchString(label) {
			return false
		}
	}
	return len(host) <= 253
}

// ResolveInfraSubscriptions uses historical AKS audit IDs only for missing infra
// subscriptions. Failures preserve discovery diagnostics and never use accounts
// or naming conventions as subscription evidence.
func ResolveInfraSubscriptions(ctx context.Context, client *http.Client, credential azcore.TokenCredential, snapshot *Snapshot) {
	missing := map[int]bool{}
	for i, g := range snapshot.Groups {
		if g.Category == "Infra" && !discoveryUUID.MatchString(g.SubscriptionID) {
			missing[i] = true
		}
	}
	if len(missing) == 0 {
		return
	}
	l := snapshot.infraLookup
	start, end := snapshot.Job.InfraStartedAt, snapshot.Job.InfraEndedAt
	jobStart, jobEnd := snapshot.Job.StartedAt, snapshot.Job.FinishedAt
	if start.IsZero() || end.IsZero() || !end.After(start) || start.Before(jobStart) || end.After(jobEnd) {
		start, end = jobStart, jobEnd
	}
	unavailable := ""
	switch {
	case l == nil || l.invalid || !kustoEndpoint(l.endpoint) || strings.TrimSpace(l.database) == "":
		unavailable = "Kusto lookup unavailable: provide the run's provision config with valid kusto.kustoName, location and serviceLogsDatabase, plus exact AKS names in config or steps"
	case jobStart.IsZero() || jobEnd.IsZero() || !jobEnd.After(jobStart) || start.IsZero() || !end.After(start):
		unavailable = "Kusto lookup unavailable: valid bounded job start/end timestamps are required"
	case credential == nil:
		unavailable = "Kusto lookup unavailable: an Azure CLI token credential with service log database read access is required"
	}
	if unavailable != "" {
		for i := range snapshot.Groups {
			if missing[i] {
				snapshot.Diagnose("error", "infra-subscription-lookup", snapshot.Groups[i].Name, unavailable)
			}
		}
		return
	}
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	httpClient := *client
	httpClient.Jar = nil
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	proposed, failures := map[int]string{}, map[int]string{}
	for _, binding := range l.bindings {
		var linked []int
		needed := false
		for i, g := range snapshot.Groups {
			if g.Category != "Infra" {
				continue
			}
			if g.Owner == binding.owner && (g.Kind == "primary" && strings.EqualFold(g.Name, binding.rg) || g.Kind == "aks-managed" && strings.EqualFold(g.Name, binding.nodeRG)) ||
				g.Owner == "Regional" && g.Kind == "primary" && binding.regionalRG != "" && strings.EqualFold(g.Name, binding.regionalRG) {
				linked = append(linked, i)
				needed = needed || missing[i]
			}
		}
		if !needed {
			continue
		}
		var sub string
		var err error
		if binding.conflict {
			err = errors.New("conflicting artifact AKS bindings; provide consistent config and steps for this run")
		} else {
			sub, err = lookupAKSSubscription(ctx, &httpClient, credential, l, binding, start, end)
		}
		if err == nil {
			for _, i := range linked {
				g := snapshot.Groups[i]
				if discoveryUUID.MatchString(g.SubscriptionID) && !strings.EqualFold(g.SubscriptionID, sub) {
					err = errors.New("historical AKS subscription contradicts an artifact subscription for a linked resource group; existing IDs retained")
					snapshot.Diagnose("error", "infra-subscription-conflict", g.Name, err.Error())
				}
			}
		}
		for _, i := range linked {
			if !missing[i] {
				continue
			}
			if err != nil {
				failures[i] = err.Error()
			} else if previous := proposed[i]; previous != "" && previous != sub {
				failures[i] = "Historical AKS bindings disagree on this resource group's subscription; no subscription selected"
			} else {
				proposed[i] = sub
			}
		}
	}
	resolved := map[string]bool{}
	for i := range snapshot.Groups {
		if !missing[i] {
			continue
		}
		g := &snapshot.Groups[i]
		message := failures[i]
		if message == "" && proposed[i] == "" {
			message = "Kusto lookup unavailable: no exact AKS binding for this group in the run's provision config or steps; provide those artifacts"
		}
		if message != "" {
			snapshot.Diagnose("error", "infra-subscription-lookup", g.Name, message)
			continue
		}
		g.SubscriptionID = proposed[i]
		resolved[strings.ToLower(g.SubscriptionID+"/"+g.Name)] = true
		g.BillingStatus = "pending"
		// Other inventory names/types may have lost nested or extension scope.
		// Only exact AKS IDs can be reconstructed from the lookup binding.
		for _, binding := range l.bindings {
			if binding.conflict || binding.owner != g.Owner || !strings.EqualFold(binding.rg, g.Name) || g.Kind != "primary" {
				continue
			}
			for j := range g.Resources {
				r := &g.Resources[j]
				if r.ID == "" && strings.EqualFold(r.Name, binding.cluster) && strings.EqualFold(r.Type, "Microsoft.ContainerService/managedClusters") {
					r.ID = "/subscriptions/" + g.SubscriptionID + "/resourceGroups/" + g.Name + "/providers/Microsoft.ContainerService/managedClusters/" + binding.cluster
				}
			}
		}
		g.Attribution = "Subscription resolved from historical AKS audit logs for this run's exact cluster and resource group"
		if g.Kind == "aks-managed" {
			g.Attribution += "; AKS node resource group inherits its parent cluster's subscription"
		} else if g.Owner == "Regional" {
			g.Attribution += "; regional infrastructure uses the configured service cluster subscription in this job's pipeline"
		}
		replaced := false
		for j := range snapshot.Diagnostics {
			d := &snapshot.Diagnostics[j]
			if !strings.EqualFold(d.Scope, g.Name) {
				continue
			}
			if d.Code == "ownership-conflict" {
				g.BillingStatus = "unavailable"
			}
			if d.Code == "missing-infra-subscription" {
				d.Severity, d.Code, d.Message = "info", "infra-subscription-resolved", g.Attribution
				replaced = true
			}
		}
		if !replaced {
			snapshot.Diagnose("info", "infra-subscription-resolved", g.Name, g.Attribution)
		}
	}
	reconcileResolvedInfraGroups(snapshot, resolved)
}

// Masked and unmasked artifacts can register separate claims that become the
// same group only after lookup. Never coalesce genuinely different ownership.
func reconcileResolvedInfraGroups(snapshot *Snapshot, resolved map[string]bool) {
	claims := map[string][]int{}
	for i, g := range snapshot.Groups {
		key := strings.ToLower(g.SubscriptionID + "/" + g.Name)
		if resolved[key] {
			claims[key] = append(claims[key], i)
		}
	}
	removed := map[int]bool{}
	for _, indices := range claims {
		if len(indices) < 2 {
			continue
		}
		g := &snapshot.Groups[indices[0]]
		conflict := false
		for _, i := range indices[1:] {
			other := snapshot.Groups[i]
			conflict = conflict || g.Category != other.Category || g.Owner != other.Owner || g.Kind != other.Kind
		}
		if conflict {
			for _, i := range indices {
				snapshot.Groups[i].BillingStatus = "unavailable"
			}
			snapshot.Diagnose("error", "ownership-conflict", g.Name, "Resolved subscription exposes conflicting resource group category, owner or kind; billing disabled")
			continue
		}
		for _, i := range indices[1:] {
			other := snapshot.Groups[i]
			removed[i] = true
			g.Resources = append(g.Resources, other.Resources...)
			if g.Attribution == "" {
				g.Attribution = other.Attribution
			}
			if other.BillingStatus == "unavailable" {
				g.BillingStatus = "unavailable"
			}
		}
		// Index full IDs first, so an ID-less resource is matched by name/type
		// only when there is a unique candidate, not an arbitrary nested scope.
		resources := g.Resources
		g.Resources = nil
		for _, withID := range []bool{true, false} {
			for _, resource := range resources {
				if (resource.ID != "") != withID {
					continue
				}
				match := -1
				for i, existing := range g.Resources {
					if resource.ID != "" && strings.EqualFold(resource.ID, existing.ID) ||
						resource.ID == "" && resource.Name != "" && resource.Type != "" && strings.EqualFold(resource.Name, existing.Name) && strings.EqualFold(resource.Type, existing.Type) {
						if match >= 0 {
							match = -1
							break
						}
						match = i
					}
				}
				if match < 0 {
					g.Resources = append(g.Resources, resource)
					continue
				}
				existing := &g.Resources[match]
				if existing.Name == "" {
					existing.Name = resource.Name
				}
				if existing.Type == "" {
					existing.Type = resource.Type
				}
				if existing.Source == "" {
					existing.Source = resource.Source
				} else if resource.Source != "" && existing.Source != resource.Source {
					existing.Source = "both"
				}
				for _, charge := range resource.Charges {
					if !slices.Contains(existing.Charges, charge) {
						existing.Charges = append(existing.Charges, charge)
					}
				}
			}
		}
	}
	groups := snapshot.Groups[:0]
	for i, g := range snapshot.Groups {
		if !removed[i] {
			groups = append(groups, g)
		}
	}
	snapshot.Groups = groups
}

func lookupAKSSubscription(ctx context.Context, client *http.Client, credential azcore.TokenCredential, lookup *infraLookup, binding infraBinding, start, end time.Time) (string, error) {
	if !discoveryRG.MatchString(binding.rg) || !kustoAKSName.MatchString(binding.cluster) {
		return "", errors.New("kusto lookup unavailable: invalid exact AKS resource group or cluster name")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	suffix := strings.ToLower("/resourcegroups/" + binding.rg + "/providers/microsoft.containerservice/managedclusters/" + binding.cluster)
	literal, _ := json.Marshal(suffix)
	csl := fmt.Sprintf("kubeAudit | where timestamp between (datetime(%s) .. datetime(%s)) | where resourceId endswith %s | summarize by resourceId", start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano), literal)
	body, _ := json.Marshal(map[string]string{"db": lookup.database, "csl": csl})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, lookup.endpoint+"/v1/rest/query", bytes.NewReader(body))
	if err != nil {
		return "", errors.New("kusto request could not be constructed")
	}
	token, err := credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{"https://kusto.kusto.windows.net/.default"}})
	if err != nil || token.Token == "" {
		return "", errors.New("kusto authentication failed; check Azure CLI login and service log database read access (details omitted)")
	}
	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("kusto HTTP request failed or was cancelled (endpoint and response details omitted)")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("kusto HTTP %d; verify service log database access (response details omitted)", resp.StatusCode)
	}
	const limit = 8 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || len(data) > limit {
		return "", errors.New("kusto response unreadable or exceeds 8 MiB; no partial results used")
	}
	ids, err := kustoResourceIDs(data)
	if err != nil {
		return "", err
	}
	sub := ""
	for _, id := range ids {
		parsed, err := azcorearm.ParseResourceID(id)
		if err != nil || !discoveryUUID.MatchString(parsed.SubscriptionID) ||
			!strings.EqualFold(parsed.ResourceType.String(), "Microsoft.ContainerService/managedClusters") ||
			!strings.EqualFold(parsed.ResourceGroupName, binding.rg) || !strings.EqualFold(parsed.Name, binding.cluster) ||
			!strings.EqualFold(id, "/subscriptions/"+parsed.SubscriptionID+suffix) {
			return "", errors.New("kusto returned an invalid or unmatched AKS ARM resource ID; no subscription selected")
		}
		value := strings.ToLower(parsed.SubscriptionID)
		if sub != "" && sub != value {
			return "", errors.New("historical AKS audit logs match multiple subscriptions for this exact binding; no subscription selected")
		}
		sub = value
	}
	if sub == "" {
		return "", errors.New("no historical AKS audit resource ID matched the exact resource group and cluster during this job; check log retention and artifact coverage")
	}
	return sub, nil
}

// Kusto v1 names result tables Table_0, etc. The table of contents identifies
// PrimaryResult and QueryStatus; successful HTTP alone does not prove completion.
func kustoResourceIDs(data []byte) ([]string, error) {
	bad := errors.New("kusto returned malformed, failed or partial query results (response details omitted)")
	var envelope map[string]json.RawMessage
	if json.Unmarshal(data, &envelope) != nil {
		return nil, bad
	}
	var tables []struct {
		TableName string
		Columns   []struct{ ColumnName, ColumnType string }
		Rows      [][]json.RawMessage
	}
	var metadata any
	_ = json.Unmarshal(data, &metadata)
	hasError := false
	walkDiscovery(metadata, func(m map[string]any) {
		for key, value := range m {
			if strings.Contains(strings.ToLower(key), "error") || strings.EqualFold(key, "Exceptions") {
				switch v := value.(type) {
				case nil:
				case bool:
					hasError = hasError || v
				case []any:
					hasError = hasError || len(v) != 0
				default:
					hasError = true
				}
			}
		}
	})
	if hasError {
		return nil, bad
	}
	if json.Unmarshal(envelope["Tables"], &tables) != nil || len(tables) == 0 {
		return nil, bad
	}
	primary, status := -1, -1
	for _, table := range tables {
		columns := map[string]int{}
		for i, column := range table.Columns {
			if _, exists := columns[column.ColumnName]; exists {
				return nil, bad
			}
			columns[column.ColumnName] = i
		}
		for _, row := range table.Rows {
			if len(row) != len(table.Columns) {
				return nil, bad
			}
		}
		kindCol, hasKind := columns["Kind"]
		nameCol, hasName := columns["Name"]
		ordinalCol, hasOrdinal := columns["Ordinal"]
		if !hasKind || !hasName || !hasOrdinal {
			continue
		}
		for _, row := range table.Rows {
			var kind, name string
			var ordinal int
			if json.Unmarshal(row[kindCol], &kind) != nil || json.Unmarshal(row[nameCol], &name) != nil || json.Unmarshal(row[ordinalCol], &ordinal) != nil || string(row[ordinalCol]) == "null" || ordinal < 0 || ordinal >= len(tables) {
				return nil, bad
			}
			if kind == "QueryResult" && name == "PrimaryResult" {
				if primary != -1 {
					return nil, bad
				}
				primary = ordinal
			}
			if kind == "QueryStatus" || name == "QueryStatus" {
				if status != -1 {
					return nil, bad
				}
				status = ordinal
			}
		}
	}
	if primary < 0 || status < 0 || primary == status {
		return nil, bad
	}
	statusTable := tables[status]
	severityCol := -1
	for i, column := range statusTable.Columns {
		if column.ColumnName == "Severity" {
			severityCol = i
		}
	}
	if severityCol < 0 || len(statusTable.Rows) == 0 {
		return nil, bad
	}
	for _, row := range statusTable.Rows {
		var severity int
		if json.Unmarshal(row[severityCol], &severity) != nil || string(row[severityCol]) == "null" || severity < 3 || severity > 6 {
			return nil, bad
		}
	}
	result := tables[primary]
	if len(result.Columns) != 1 || result.Columns[0].ColumnName != "resourceId" || result.Columns[0].ColumnType != "string" {
		return nil, bad
	}
	var ids []string
	for _, row := range result.Rows {
		var id string
		if json.Unmarshal(row[0], &id) != nil || id == "" {
			return nil, bad
		}
		ids = append(ids, id)
	}
	return ids, nil
}
