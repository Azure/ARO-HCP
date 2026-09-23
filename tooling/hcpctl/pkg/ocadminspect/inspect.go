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

// Package ocadminspect implements a Kusto-backed equivalent of `oc adm inspect`:
// given a management/service cluster, a namespace, and a point in time, it
// reconstructs the state of every resource in that namespace (from
// kubernetesResourceSnapshots), the namespace's Kubernetes events (from
// kubernetesEvents), and the container logs for the namespace's pods up to that
// time (from containerLogs), writing them to disk in an oc-adm-inspect-style
// layout. Unlike `oc adm inspect`, it queries historical telemetry rather than a
// live cluster, so it can inspect a namespace as it existed at a past timestamp.
package ocadminspect

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

// Builtin query definition names (registered in pkg/kusto/templates/builtin/queries.yaml).
const (
	resourcesQueryName      = "ocAdmInspectResources"
	eventsQueryName         = "ocAdmInspectEvents"
	activeClustersQueryName = "ocAdmInspectActiveClusters"
	namespacesQueryName     = "ocAdmInspectNamespaces"
)

// containerLogSourceQueries are the container-log queries run per namespace, one
// per database: pods in management-cluster namespaces log to ServiceLogs, while
// hosted control plane pods log to HostedControlPlaneLogs. Both are queried and
// merged so a namespace's logs are captured wherever the forwarder routed them.
var containerLogSourceQueries = []string{
	"ocAdmInspectContainerLogs",          // ServiceLogs
	"ocAdmInspectHostedControlPlaneLogs", // HostedControlPlaneLogs
}

// ManagementClusterNameMarker is the substring that distinguishes a management
// cluster name from a service cluster name in the `cluster` telemetry column.
const ManagementClusterNameMarker = "mgmt"

// QueryExecutor is the subset of the Kusto query client the inspector needs. It
// is satisfied by *mustgather.QueryClient.
type QueryExecutor interface {
	ExecutePreconfiguredQuery(ctx context.Context, query kusto.Query, outputChannel chan<- kusto.TaggedRow) (*kusto.QueryResult, error)
}

// Resource is a single Kubernetes object reconstructed as of the requested time.
type Resource struct {
	APIVersion string
	Kind       string
	Namespace  string
	Name       string
	// Object is the full Kubernetes object as stored in the snapshot.
	Object map[string]any
}

// LogLine is one container log line with its Kusto timestamp.
type LogLine struct {
	Timestamp string
	// Log is the log payload (typically a map with a "log" string, matching the
	// containerLogs projection).
	Log any
}

// Writer persists what the inspector gathers for a namespace. Implementations
// choose the on-disk layout.
type Writer interface {
	// WriteResources writes all resources gathered for a namespace.
	WriteResources(ctx context.Context, namespace string, resources []Resource) error
	// WriteEvents writes the namespace's Kubernetes events (each event is a
	// column->value map from the events query).
	WriteEvents(ctx context.Context, namespace string, events []map[string]any) error
	// WriteContainerLog writes the log lines for a single pod container.
	WriteContainerLog(ctx context.Context, namespace, pod, container string, lines []LogLine) error
	// NamespaceOutputPath returns a human-readable location (e.g. a directory
	// path) where the namespace's content is written, for logging. It may be
	// empty for writers that have no such location.
	NamespaceOutputPath(namespace string) string
}

// Inspector gathers namespace state from Kusto for a single management/service
// (infra) cluster at a fixed time window.
type Inspector struct {
	exec        QueryExecutor
	factory     *kusto.QueryFactory
	baseOptions kusto.QueryOptions
	clusterName string
	writer      Writer
}

// NewInspector builds an Inspector. clusterName is the infra (management or
// service) cluster name that appears in the telemetry `cluster` column;
// baseOptions supplies the time window (TimestampMin/Max) and Limit.
func NewInspector(exec QueryExecutor, factory *kusto.QueryFactory, baseOptions kusto.QueryOptions, clusterName string, writer Writer) *Inspector {
	return &Inspector{
		exec:        exec,
		factory:     factory,
		baseOptions: baseOptions,
		clusterName: clusterName,
		writer:      writer,
	}
}

// InspectNamespaces gathers and writes the state, events, and container logs for
// each namespace. Failures for one namespace do not abort the others; the joined
// error is returned at the end.
func (i *Inspector) InspectNamespaces(ctx context.Context, namespaces []string) error {
	logger := logr.FromContextOrDiscard(ctx)
	var errs []error
	for _, namespace := range namespaces {
		logger.Info("running oc-adm-inspect for namespace",
			"cluster", i.clusterName,
			"namespace", namespace,
			"output", i.writer.NamespaceOutputPath(namespace),
		)
		if err := i.inspectResources(ctx, namespace); err != nil {
			errs = append(errs, fmt.Errorf("failed to inspect resources in %q: %w", namespace, err))
		}
		if err := i.inspectEvents(ctx, namespace); err != nil {
			errs = append(errs, fmt.Errorf("failed to inspect events in %q: %w", namespace, err))
		}
		if err := i.inspectContainerLogs(ctx, namespace); err != nil {
			errs = append(errs, fmt.Errorf("failed to inspect container logs in %q: %w", namespace, err))
		}
	}
	return joinErrors(errs)
}

func (i *Inspector) inspectResources(ctx context.Context, namespace string) error {
	rows, err := i.runNamespaceQuery(ctx, resourcesQueryName, namespace)
	if err != nil {
		return err
	}
	resources := make([]Resource, 0, len(rows))
	for _, row := range rows {
		object, _ := row["object"].(map[string]any)
		resources = append(resources, Resource{
			APIVersion: asString(row["apiVersion"]),
			Kind:       asString(row["objectKind"]),
			Namespace:  asString(row["namespace"]),
			Name:       asString(row["name"]),
			Object:     object,
		})
	}
	return i.writer.WriteResources(ctx, namespace, resources)
}

func (i *Inspector) inspectEvents(ctx context.Context, namespace string) error {
	rows, err := i.runNamespaceQuery(ctx, eventsQueryName, namespace)
	if err != nil {
		return err
	}
	return i.writer.WriteEvents(ctx, namespace, rows)
}

func (i *Inspector) inspectContainerLogs(ctx context.Context, namespace string) error {
	type podContainer struct{ pod, container string }
	byContainer := make(map[podContainer][]LogLine)

	var errs []error
	// Query every container-log source (ServiceLogs + HostedControlPlaneLogs) and
	// merge, so control-plane pod logs are captured alongside management-cluster
	// namespace logs.
	for _, defName := range containerLogSourceQueries {
		rows, err := i.runNamespaceQuery(ctx, defName, namespace)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to query container logs (%s): %w", defName, err))
			continue
		}
		for _, row := range rows {
			key := podContainer{pod: asString(row["pod_name"]), container: asString(row["container_name"])}
			byContainer[key] = append(byContainer[key], LogLine{
				Timestamp: asString(row["timestamp"]),
				Log:       row["log"],
			})
		}
	}

	// Write deterministically: pods/containers in sorted order, each container's
	// lines re-sorted by timestamp since they may come from two merged sources.
	keys := make([]podContainer, 0, len(byContainer))
	for key := range byContainer {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(a, b int) bool {
		if keys[a].pod != keys[b].pod {
			return keys[a].pod < keys[b].pod
		}
		return keys[a].container < keys[b].container
	})
	for _, key := range keys {
		lines := byContainer[key]
		sort.SliceStable(lines, func(a, b int) bool { return lines[a].Timestamp < lines[b].Timestamp })
		if err := i.writer.WriteContainerLog(ctx, namespace, key.pod, key.container, lines); err != nil {
			errs = append(errs, fmt.Errorf("failed to write logs for pod %q container %q: %w", key.pod, key.container, err))
		}
	}
	return joinErrors(errs)
}

// ExpandWithPairedNamespaces returns the requested namespaces plus, for any that
// is a hosted-cluster namespace or a control-plane namespace on this cluster, its
// paired counterpart. It discovers the cluster's namespaces and pairs them by the
// hosted/control-plane prefix relationship (control-plane = <hostedNamespace>-<name>).
// Ordering is deterministic (sorted) and duplicates removed.
func (i *Inspector) ExpandWithPairedNamespaces(ctx context.Context, requested []string) ([]string, error) {
	clusterNamespaces, err := i.DiscoverNamespaces(ctx, nil)
	if err != nil {
		return nil, err
	}
	return pairNamespaces(requested, clusterNamespaces), nil
}

// hostedClusterNamespacePrefix is the prefix of hosted-cluster and control-plane
// namespaces (ocm-<env>-<cid>[-<name>]). Pairing is restricted to namespaces with
// this prefix so unrelated namespaces that merely share a name prefix (e.g.
// "kube-system" and "kube-system-x") are never paired.
const hostedClusterNamespacePrefix = "ocm-"

// pairNamespaces returns the requested namespaces plus, for any that is a
// hosted-cluster namespace or a control-plane namespace among clusterNamespaces,
// its counterpart. A control-plane namespace is <hostedNamespace>-<name>, so the
// two are paired when one is the other with a trailing "-<suffix>". Only namespaces
// with the hosted-cluster prefix are considered, so unrelated namespaces that share
// a name prefix are not falsely paired. Deterministic, de-duplicated. Pure function.
func pairNamespaces(requested, clusterNamespaces []string) []string {
	set := make(map[string]struct{}, len(requested))
	for _, ns := range requested {
		set[ns] = struct{}{}
	}
	for _, ns := range requested {
		if !strings.HasPrefix(ns, hostedClusterNamespacePrefix) {
			continue
		}
		for _, other := range clusterNamespaces {
			if other == "" || other == ns || !strings.HasPrefix(other, hostedClusterNamespacePrefix) {
				continue
			}
			// other is ns's control-plane namespace, or ns is other's.
			if strings.HasPrefix(other, ns+"-") || strings.HasPrefix(ns, other+"-") {
				set[other] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(set))
	for ns := range set {
		if ns != "" {
			out = append(out, ns)
		}
	}
	sort.Strings(out)
	return out
}

// DiscoverNamespaces returns the distinct namespaces on this cluster in the time
// window. When clusterIDs is non-empty it restricts to namespaces that embed one
// of those cluster IDs (both the hosted-cluster namespace ocm-<env>-<cid> and its
// control-plane namespace ocm-<env>-<cid>-<name> contain the cid), which is how
// the namespaces for a resource group's clusters are found.
func (i *Inspector) DiscoverNamespaces(ctx context.Context, clusterIDs []string) ([]string, error) {
	logger := logr.FromContextOrDiscard(ctx)
	def, err := i.factory.GetBuiltinQueryDefinition(namespacesQueryName)
	if err != nil {
		return nil, err
	}
	data := kusto.NewTemplateDataFromOptions(i.baseOptions,
		kusto.WithClusterName(i.clusterName),
		kusto.WithClusterIds(clusterIDs),
	)
	queries, err := i.factory.Build(*def, data)
	if err != nil {
		return nil, err
	}
	rows, err := runQuery(ctx, i.exec, queries[0])
	if err != nil {
		return nil, err
	}
	namespaces := make([]string, 0, len(rows))
	for _, row := range rows {
		if namespace := asString(row["namespace"]); namespace != "" {
			namespaces = append(namespaces, namespace)
		}
	}
	logger.Info("oc-adm-inspect: discovered namespaces",
		"cluster", i.clusterName,
		"namespaces", namespaces,
	)
	return namespaces, nil
}

// DiscoverActiveClusters returns the distinct cluster names seen in backend logs
// in the time window, for telling the user which clusters are available when they
// did not specify one. It does not require a cluster to be set, so it is a package
// function rather than an Inspector method.
func DiscoverActiveClusters(ctx context.Context, exec QueryExecutor, factory *kusto.QueryFactory, baseOptions kusto.QueryOptions) ([]string, error) {
	def, err := factory.GetBuiltinQueryDefinition(activeClustersQueryName)
	if err != nil {
		return nil, err
	}
	queries, err := factory.Build(*def, kusto.NewTemplateDataFromOptions(baseOptions))
	if err != nil {
		return nil, err
	}
	rows, err := runQuery(ctx, exec, queries[0])
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(rows))
	clusters := make([]string, 0, len(rows))
	for _, row := range rows {
		clusterName := clusterNameFromNameOrResourceID(asString(row["cluster_name"]))
		if clusterName == "" {
			continue
		}
		if _, ok := seen[clusterName]; ok {
			continue
		}
		seen[clusterName] = struct{}{}
		clusters = append(clusters, clusterName)
	}
	sort.Strings(clusters)
	return clusters, nil
}

// clusterNameFromNameOrResourceID returns the cluster name from a value that may
// be either a bare cluster name or a full ARM resource ID (whose last path
// segment is the cluster name).
func clusterNameFromNameOrResourceID(nameOrID string) string {
	if idx := strings.LastIndex(nameOrID, "/"); idx >= 0 {
		return nameOrID[idx+1:]
	}
	return nameOrID
}

// IsManagementCluster reports whether an infra cluster name denotes a management
// cluster (as opposed to a service cluster).
func IsManagementCluster(clusterName string) bool {
	return strings.Contains(clusterName, ManagementClusterNameMarker)
}

// DiscoverResourceGroupClusterIDs returns the HCP cluster IDs (the `cid` column)
// that have Cluster Service logs scoped to baseOptions' subscription/resource
// group, using the shared "clusterId" discovery query.
func DiscoverResourceGroupClusterIDs(ctx context.Context, exec QueryExecutor, factory *kusto.QueryFactory, baseOptions kusto.QueryOptions) ([]string, error) {
	return discoverDistinctColumn(ctx, exec, factory, baseOptions, "clusterId", "cid")
}

// DiscoverResourceGroupClusterNames returns the infra (management/service)
// cluster names that emitted logs for baseOptions' subscription/resource group in
// the time window, using the shared "clusterNamesSvc"/"clusterNamesHcp" queries.
func DiscoverResourceGroupClusterNames(ctx context.Context, exec QueryExecutor, factory *kusto.QueryFactory, baseOptions kusto.QueryOptions) ([]string, error) {
	seen := make(map[string]struct{})
	var names []string
	for _, defName := range []string{"clusterNamesSvc", "clusterNamesHcp"} {
		values, err := discoverDistinctColumn(ctx, exec, factory, baseOptions, defName, "cluster")
		if err != nil {
			return nil, err
		}
		for _, value := range values {
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			names = append(names, value)
		}
	}
	sort.Strings(names)
	return names, nil
}

// InspectResourceGroupManagementNamespaces inspects, for every HCP cluster in
// baseOptions' resource group, its namespaces (the hosted-cluster namespace and
// its control-plane namespace, discovered by matching the cluster id embedded in
// the namespace name) on whichever management cluster hosts it. This is the entry
// point the must-gather command uses. Per-cluster failures are collected and
// returned together so one bad cluster does not abort the rest.
//
// clusterIDs, when non-empty, are the cluster IDs to inspect (wired from
// must-gather's --cluster-id); when empty they are discovered from the resource
// group's Cluster Service logs.
func InspectResourceGroupManagementNamespaces(ctx context.Context, exec QueryExecutor, factory *kusto.QueryFactory, baseOptions kusto.QueryOptions, writer Writer, clusterIDs []string) error {
	logger := logr.FromContextOrDiscard(ctx)

	if len(clusterIDs) == 0 {
		discovered, err := DiscoverResourceGroupClusterIDs(ctx, exec, factory, baseOptions)
		if err != nil {
			return fmt.Errorf("failed to discover cluster IDs: %w", err)
		}
		clusterIDs = discovered
	}
	logger.Info("oc-adm-inspect: cluster IDs to inspect", "clusterIDs", clusterIDs)
	if len(clusterIDs) == 0 {
		logger.Info("no HCP clusters discovered in resource group; skipping oc-adm-inspect",
			"resourceGroup", baseOptions.ResourceGroupName,
		)
		return nil
	}

	clusterNames, err := DiscoverResourceGroupClusterNames(ctx, exec, factory, baseOptions)
	if err != nil {
		return fmt.Errorf("failed to discover infra cluster names: %w", err)
	}
	logger.Info("oc-adm-inspect: discovered infra cluster names", "clusterNames", clusterNames)

	var errs []error
	for _, clusterName := range clusterNames {
		if !IsManagementCluster(clusterName) {
			continue
		}
		inspector := NewInspector(exec, factory, baseOptions, clusterName, writer)
		namespaces, err := inspector.DiscoverNamespaces(ctx, clusterIDs)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to discover namespaces on %q: %w", clusterName, err))
			continue
		}
		if len(namespaces) == 0 {
			continue
		}
		logger.Info("oc-adm-inspect: discovered namespaces for resource group clusters",
			"managementCluster", clusterName, "namespaceCount", len(namespaces))
		if err := inspector.InspectNamespaces(ctx, namespaces); err != nil {
			errs = append(errs, fmt.Errorf("failed to inspect namespaces on %q: %w", clusterName, err))
		}
	}
	return joinErrors(errs)
}

// discoverDistinctColumn runs a builtin discovery query and returns the distinct
// non-empty string values of one column.
func discoverDistinctColumn(ctx context.Context, exec QueryExecutor, factory *kusto.QueryFactory, baseOptions kusto.QueryOptions, defName, column string) ([]string, error) {
	def, err := factory.GetBuiltinQueryDefinition(defName)
	if err != nil {
		return nil, err
	}
	queries, err := factory.Build(*def, kusto.NewTemplateDataFromOptions(baseOptions))
	if err != nil {
		return nil, err
	}
	rows, err := runQuery(ctx, exec, queries[0])
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(rows))
	var values []string
	for _, row := range rows {
		value := asString(row[column])
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values, nil
}

func (i *Inspector) runNamespaceQuery(ctx context.Context, defName, namespace string) ([]map[string]any, error) {
	def, err := i.factory.GetBuiltinQueryDefinition(defName)
	if err != nil {
		return nil, err
	}
	data := kusto.NewTemplateDataFromOptions(i.baseOptions,
		kusto.WithClusterName(i.clusterName),
		kusto.WithNamespace(namespace),
	)
	queries, err := i.factory.Build(*def, data)
	if err != nil {
		return nil, err
	}
	return runQuery(ctx, i.exec, queries[0])
}

// runQuery executes a single query and returns each row as a column->value map,
// parsing dynamic columns (e.g. the snapshot `object`) into nested structures.
func runQuery(ctx context.Context, exec QueryExecutor, query kusto.Query) ([]map[string]any, error) {
	outputChannel := make(chan kusto.TaggedRow)
	var rows []map[string]any

	group := new(errgroup.Group)
	group.Go(func() error {
		for tagged := range outputChannel {
			rows = append(rows, rowToMap(tagged))
		}
		return nil
	})

	_, queryErr := exec.ExecutePreconfiguredQuery(ctx, query, outputChannel)
	close(outputChannel)

	if err := group.Wait(); err != nil {
		return nil, fmt.Errorf("failed to process query results: %w", err)
	}
	if queryErr != nil {
		return nil, fmt.Errorf("failed to execute query %q: %w", query.GetName(), queryErr)
	}
	return rows, nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func joinErrors(errs []error) error {
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	default:
		msgs := make([]string, 0, len(errs))
		for _, err := range errs {
			msgs = append(msgs, err.Error())
		}
		return fmt.Errorf("%d errors: %s", len(errs), strings.Join(msgs, "; "))
	}
}
