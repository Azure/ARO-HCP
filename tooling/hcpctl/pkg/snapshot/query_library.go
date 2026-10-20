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

package snapshot

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// QueryLibraryIndexFile is the name of the generated index written to the root
// of an exported query library.
const QueryLibraryIndexFile = "INDEX.md"

// queriesRoot is the root of the query library inside the embedded FS.
const queriesRoot = "queries"

// queryTemplateVariable documents one field of queryData that KQL templates
// may reference.
type queryTemplateVariable struct {
	name        string
	description string
}

// queryTemplateVariables documents every exported field of queryData so an
// investigator without a gathered snapshot knows what to substitute for each
// template variable and where to discover its value.
var queryTemplateVariables = []queryTemplateVariable{
	{"ClusterURI", "Kusto cluster URI that holds the ARO-HCP logs (for example `https://<name>.<region>.kusto.windows.net`)."},
	{"ServiceDatabase", "Service log database (normally `ServiceLogs`): frontend, backend, Clusters Service, Maestro, mgmt-agent and HyperShift operator logs, Kubernetes events, audit logs, and resource/Cosmos snapshots."},
	{"HCPDatabase", "Hosted control plane log database (normally `HostedControlPlaneLogs`): per-cluster control plane container logs."},
	{"MonitoringEventsDatabase", "Alert database (normally `MonitoringEvents`)."},
	{"ResourceGroup", "Customer ARM resource group of the affected cluster. Discover it from the cluster's ARM resource ID or from `frontend/frontendRequests`-style queries over `frontendLogs`."},
	{"ServiceClusterName", "Optional AKS service cluster name (the `cluster` column). Only used inside `{{ if and .ServiceClusterName .ManagementClusterName }}` blocks; drop those blocks when the names are unknown."},
	{"ManagementClusterName", "Optional AKS management cluster name (the `cluster` column). See `ServiceClusterName`."},
	{"FullStartTime", "Start of the overall investigation window. Rendered with `kqlDatetime`, so substitute a `datetime(...)` literal."},
	{"FullEndTime", "End of the overall investigation window. Rendered with `kqlDatetime`."},
	{"PhaseStartTime", "Start of a narrower window of interest inside the overall window (snapshots use this for the test or cleanup phase). Without a narrower window, use the overall start time."},
	{"PhaseEndTime", "End of the narrower window of interest. Without a narrower window, use the overall end time."},
	{"PhaseName", "Human-readable phase name (`test` or `cleanup`) used by the snapshot gatherer; not referenced by templates."},
	{"CorrelationID", "ARM correlation request ID of one request, from `frontendLogs` (`log.correlation_request_id`)."},
	{"ClientRequestID", "ARM client request ID of one request, from `frontend/clientRequestId` or `frontendLogs` (`log.client_request_id`)."},
	{"ResponseStatusCode", "HTTP response status code of one request, from `frontendLogs` (`log.response_status_code`); used by the gatherer to decide which follow-up queries apply."},
	{"SubscriptionID", "Customer subscription ID, taken from the ARM resource ID."},
	{"ResourceID", "Full ARM resource ID of the resource under investigation (cluster, nodepool, ...), from `frontendLogs` (`log.resource_id`)."},
	{"ResourceType", "ARM resource type of `ResourceID`, for example `microsoft.redhatopenshift/hcpopenshiftclusters` or `microsoft.redhatopenshift/hcpopenshiftclusters/nodepools`."},
	{"ResourceName", "Name segment of `ResourceID`."},
	{"ClusterResourceID", "ARM resource ID of the HCP cluster that owns `ResourceID` (equal to `ResourceID` for a cluster)."},
	{"ClusterResourceName", "Name of the HCP cluster that owns `ResourceID`."},
	{"ServiceProviderResourceType", "Cosmos service provider type for `ResourceType`: `microsoft.redhatopenshift/hcpopenshiftclusters/serviceproviderclusters` for clusters, `microsoft.redhatopenshift/hcpopenshiftclusters/nodepools/serviceprovidernodepools` for nodepools."},
	{"AsyncOperationId", "Async operation ID of a mutating request, from `frontend/asyncOperationId`."},
	{"AsyncOperationPath", "Async operation status path of a mutating request, from `frontend/asyncOperationPath`."},
	{"InternalID", "Clusters Service internal ID path of the resource, from `backend/resourceInternalId`."},
	{"ClusterID", "Clusters Service cluster ID (`cid`), from `clustersService/cid`."},
	{"HostedClusterNamespace", "Management cluster namespace of the HostedCluster (`ocm-arohcp<env>-<cid>`), from `hypershift/hostedClusterMetadata`."},
	{"HostedControlPlaneNamespace", "Management cluster namespace of the hosted control plane (`ocm-arohcp<env>-<cid>-<name>`), from `hypershift/hostedClusterMetadata`."},
	{"ChildResourceTypes", "Child resource types present under the cluster in `cosmosResourceSnapshots`, from `backend/cosmosResourceDiscovery`; used by the gatherer to decide which queries apply, not referenced by templates."},
}

// readmeReferenceDir maps a query category to the directory name that the
// snapshot gatherer writes its output under. READMEs refer to sibling queries
// by these snapshot-relative paths (for example
// `conditions/hypershift/hostedClusterConditionTimeline.md`).
func readmeReferenceDir(cat queryCategory) string {
	switch cat {
	case categoryContext:
		return "context"
	case categoryRequestDiscovery, categoryResourceDiscovery:
		return "discovery"
	case categoryState, categoryTraceState:
		return "state"
	case categoryConditions:
		return "conditions"
	case categoryLogs, categoryTraceLogs:
		return "logs"
	case categoryEvents, categoryResourceEvents:
		return "events"
	default:
		return ""
	}
}

// databaseVariable maps a querySpec database key to the template variable that
// names that database.
func databaseVariable(database string) string {
	switch database {
	case "hcp":
		return "HCPDatabase"
	case "monitoringEvents":
		return "MonitoringEventsDatabase"
	default:
		return "ServiceDatabase"
	}
}

// WriteQueryLibrary writes the complete embedded snapshot query library —
// every README, KQL template, and shared partial — to dir, preserving its
// directory layout (dir/<component>/<queryName>/{README.md,query.kql} and
// dir/partials/*.kql). It also writes a generated INDEX.md describing each
// query, its target database, its prerequisites, and every template variable.
func WriteQueryLibrary(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create query library directory: %w", err)
	}
	err := fs.WalkDir(queriesFS, queriesRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, queriesRoot), "/")
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := queriesFS.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		return fmt.Errorf("failed to write query library: %w", err)
	}

	index, err := QueryLibraryIndex()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, QueryLibraryIndexFile), []byte(index), 0o644); err != nil {
		return fmt.Errorf("failed to write query library index: %w", err)
	}
	return nil
}

// QueryLibraryIndex renders a Markdown index of the embedded query library.
func QueryLibraryIndex() (string, error) {
	specsByPath := make(map[string]querySpec, len(allQueries))
	for _, q := range allQueries {
		specsByPath[q.templatePath] = q
	}

	var templates []string
	err := fs.WalkDir(queriesFS, queriesRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == "query.kql" {
			templates = append(templates, p)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to list query templates: %w", err)
	}
	sort.Strings(templates)

	partials, err := fs.Glob(queriesFS, queriesRoot+"/partials/*.kql")
	if err != nil {
		return "", fmt.Errorf("failed to list partial templates: %w", err)
	}
	sort.Strings(partials)

	var sb strings.Builder
	sb.WriteString("# ARO-HCP Query Library\n\n")
	sb.WriteString("These are the KQL templates the `hcpctl snapshot` gatherer runs, each with a README. ")
	sb.WriteString("Every query lives in `<component>/<queryName>/`:\n\n")
	sb.WriteString("- `README.md` — **Summary** (what the query shows), **What to Look For** (how to read the output), ")
	sb.WriteString("and **Where to Go Next** (follow-up queries).\n")
	sb.WriteString("- `query.kql` — a Go `text/template` that renders to concrete KQL.\n\n")

	sb.WriteString("## Rendering a template by hand\n\n")
	sb.WriteString("- Replace every `{{ .Variable }}` with a concrete value (see Template Variables below).\n")
	sb.WriteString("- Replace `{{ kqlDatetime .X }}` with a KQL literal such as `datetime(2026-01-02T03:04:05Z)`.\n")
	sb.WriteString("- Replace `{{ toLower .X }}` with the lower-cased value.\n")
	sb.WriteString("- Resolve `{{ if ... }}` / `{{ else }}` / `{{ end }}` blocks: keep the branch whose condition holds for your values ")
	sb.WriteString("and delete the others. Delete optional filters whose values you do not know.\n")
	sb.WriteString("- Replace `{{ template \"name\" . }}` with the matching `{{ define \"name\" }}` body from `partials/`, rendered the same way.\n")
	sb.WriteString("- `{{-` and `-}}` only trim surrounding whitespace.\n")
	sb.WriteString("- The result must contain no `{{` or `}}`.\n\n")

	sb.WriteString("READMEs refer to related queries by the path the snapshot gatherer writes their output to, for example ")
	sb.WriteString("`conditions/hypershift/hostedClusterConditionTimeline.md`. Such a path maps to the library directory ")
	sb.WriteString("`<component>/<queryName>/` — here `hypershift/hostedClusterConditionTimeline/`. The **README reference** column below lists these paths. ")
	sb.WriteString("READMEs may also mention snapshot phases (`test/`, `cleanup/`) or a phase `manifest.json`; ")
	sb.WriteString("those only exist in gathered snapshots, so substitute your own time window.\n\n")

	sb.WriteString("## Template Variables\n\n")
	sb.WriteString("| Variable | Meaning and where to discover it |\n")
	sb.WriteString("|----------|----------------------------------|\n")
	for _, v := range queryTemplateVariables {
		sb.WriteString(fmt.Sprintf("| `.%s` | %s |\n", v.name, escapeTableCell(v.description)))
	}
	sb.WriteString("\n")

	sb.WriteString("## Shared Partials\n\n")
	for _, p := range partials {
		sb.WriteString(fmt.Sprintf("- `%s`\n", strings.TrimPrefix(p, queriesRoot+"/")))
	}
	sb.WriteString("\n")

	sb.WriteString("## Queries\n\n")
	sb.WriteString("| Query | Database | README reference | Prerequisites |\n")
	sb.WriteString("|-------|----------|------------------|---------------|\n")
	for _, t := range templates {
		dir := path.Dir(strings.TrimPrefix(t, queriesRoot+"/"))
		database, reference, prerequisites := "`.ServiceDatabase`", "—", "—"
		if spec, ok := specsByPath[t]; ok {
			database = "`." + databaseVariable(spec.database) + "`"
			if refDir := readmeReferenceDir(spec.category); refDir != "" {
				reference = fmt.Sprintf("`%s/%s/%s.md`", refDir, spec.component, spec.queryName)
			}
			if spec.prerequisites != "" {
				prerequisites = spec.prerequisites
			}
		}
		sb.WriteString(fmt.Sprintf("| `%s/` | %s | %s | %s |\n", dir, database, reference, escapeTableCell(prerequisites)))
	}

	return sb.String(), nil
}

// escapeTableCell makes s safe to embed in a Markdown table cell.
func escapeTableCell(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}
