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

package entitygroups

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/tools/cmdutils"
	kustoazure "github.com/Azure/ARO-Tools/tools/kustoctl/internal/azure"
	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/kql"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
)

var (
	egNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	dbNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	envPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// entityGroup is a parsed entity group definition.
type entityGroup struct {
	name     string
	database string
}

// RawSyncOptions represents the initial, unvalidated configuration.
type RawSyncOptions struct {
	EntityGroups []string
	Timeout      time.Duration
	ARMEndpoint  string
	AADAuthority string
	// Environment optionally scopes cluster discovery to a single ARO-HCP
	// environment (for example int, stg or prod). When set, only clusters whose
	// scope tag (ScopeTagKey, by default aroHCPEnvironment) matches are included,
	// so each environment gets its own isolated entity group. When empty, all
	// clusters carrying the selection tag (TagKey, by default aroHCPPurpose) are
	// included (legacy cross-environment behavior).
	Environment string
	// TagKey and TagValue select which resource tag marks a Kusto cluster as a
	// sync target. TagValue is optional; when empty, presence of the key is
	// sufficient. ScopeTagKey is the tag used for per-environment scoping (its
	// value is matched against Environment). They default to the ARO-HCP tags but
	// can be overridden so other products (for example ARO Classic) can reuse this
	// tool, matching the TagKey/TagValue discovery convention already used by
	// grafanactl and hcpctl.
	TagKey      string
	TagValue    string
	ScopeTagKey string
}

type validatedSyncOptions struct {
	*RawSyncOptions
	cloudConfig     cloud.Configuration
	entityGroups    []entityGroup
	discoveryConfig kustoazure.KustoDiscoveryConfig
}

// ValidatedSyncOptions represents configuration that has passed validation.
type ValidatedSyncOptions struct {
	*validatedSyncOptions
}

// CompletedSyncOptions represents the final, fully initialized configuration.
type CompletedSyncOptions struct {
	*validatedSyncOptions
	cred     azcore.TokenCredential
	clusters []kustoazure.KustoCluster
}

// DefaultSyncOptions returns a new RawSyncOptions with default values.
func DefaultSyncOptions() *RawSyncOptions {
	return &RawSyncOptions{
		Timeout:     5 * time.Minute,
		TagKey:      "aroHCPPurpose",
		ScopeTagKey: "aroHCPEnvironment",
	}
}

// BindSyncOptions binds command-line flags to the options.
func BindSyncOptions(opts *RawSyncOptions, cmd *cobra.Command) error {
	flags := cmd.Flags()
	flags.StringArrayVar(&opts.EntityGroups, "entity-group", nil, "Entity group to sync as name:database (repeatable, required)")
	flags.DurationVar(&opts.Timeout, "timeout", opts.Timeout, "Timeout for the entire sync operation")
	flags.StringVar(&opts.ARMEndpoint, "arm-endpoint", "", "Azure Resource Manager endpoint for the target cloud (defaults to public cloud)")
	flags.StringVar(&opts.AADAuthority, "aad-authority", "", "Microsoft Entra ID authority for the target cloud (defaults to public cloud)")
	flags.StringVar(&opts.Environment, "env-tag-value", opts.Environment, "value that scopes cluster discovery to a single ARO-HCP environment (for example int, stg or prod); when set, only clusters whose env tag (--env-tag-key, default aroHCPEnvironment) equals this value are included so each environment gets an isolated entity group")
	flags.StringVar(&opts.TagKey, "tag-key", opts.TagKey, "resource tag key that marks a Kusto cluster as a sync target (default aroHCPPurpose)")
	flags.StringVar(&opts.TagValue, "tag-value", opts.TagValue, "optional required value for the selection tag; when empty, presence of the key is sufficient")
	flags.StringVar(&opts.ScopeTagKey, "env-tag-key", opts.ScopeTagKey, "resource tag key whose value identifies a cluster's environment, matched against --env-tag-value (default aroHCPEnvironment)")

	_ = cmd.MarkFlagRequired("entity-group")
	return nil
}

// resolveCloudConfig builds a cloud.Configuration from the provided ARM endpoint
// and AAD authority. When both inputs are empty, the public cloud configuration is
// returned. When both are set, a custom configuration is built with a fresh map.
// Based on the grafanactl base/options.go resolveCloudConfig pattern, with improved normalizeEndpoint (strips paths, rejects non-https).
func resolveCloudConfig(armEndpoint, aadAuthority string) (cloud.Configuration, error) {
	if armEndpoint == "" && aadAuthority == "" {
		return cloud.AzurePublic, nil
	}
	if armEndpoint == "" || aadAuthority == "" {
		return cloud.Configuration{}, fmt.Errorf("--arm-endpoint and --aad-authority must both be set, or both unset")
	}

	normalizedARM, err := normalizeEndpoint(armEndpoint)
	if err != nil {
		return cloud.Configuration{}, fmt.Errorf("invalid --arm-endpoint %q: %w", armEndpoint, err)
	}

	normalizedAAD, err := normalizeEndpoint(aadAuthority)
	if err != nil {
		return cloud.Configuration{}, fmt.Errorf("invalid --aad-authority %q: %w", aadAuthority, err)
	}

	return cloud.Configuration{
		ActiveDirectoryAuthorityHost: normalizedAAD,
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Endpoint: normalizedARM,
				Audience: normalizedARM,
			},
		},
	}, nil
}

// normalizeEndpoint accepts either a hostname or a full URL and returns a
// URL-form value with the https scheme. Follows the grafanactl pattern.
func normalizeEndpoint(endpoint string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", fmt.Errorf("endpoint cannot be empty")
	}

	// If no scheme, assume https. If a scheme is present, it must be https.
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("not a valid URL: %w", err)
	}

	if parsed.Scheme == "" {
		endpoint = "https://" + endpoint
		parsed, err = url.Parse(endpoint)
		if err != nil {
			return "", fmt.Errorf("not a valid URL after adding scheme: %w", err)
		}
	} else if parsed.Scheme != "https" {
		return "", fmt.Errorf("endpoint must use https scheme, got %q", parsed.Scheme)
	}

	if parsed.Host == "" {
		return "", fmt.Errorf("endpoint host cannot be empty")
	}

	// Return only scheme + host (strip any path/query/fragment)
	return fmt.Sprintf("https://%s", parsed.Host), nil
}

// Validate validates the raw options and returns validated options.
func (o *RawSyncOptions) Validate(_ context.Context) (*ValidatedSyncOptions, error) {
	cloudConfig, err := resolveCloudConfig(o.ARMEndpoint, o.AADAuthority)
	if err != nil {
		return nil, err
	}

	if o.Environment != "" && !envPattern.MatchString(o.Environment) {
		return nil, fmt.Errorf("invalid --env-tag-value %q; must match [A-Za-z0-9_-]+", o.Environment)
	}

	discoveryConfig := kustoazure.KustoDiscoveryConfig{
		TagKey:      o.TagKey,
		TagValue:    o.TagValue,
		ScopeTagKey: o.ScopeTagKey,
	}.WithDefaults()
	if err := discoveryConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid discovery tag configuration (--tag-key, --tag-value, --env-tag-key): %w", err)
	}

	if len(o.EntityGroups) == 0 {
		return nil, fmt.Errorf("at least one --entity-group is required")
	}

	var groups []entityGroup
	seen := make(map[string]bool)
	for _, eg := range o.EntityGroups {
		parts := strings.SplitN(eg, ":", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("invalid --entity-group format %q; expected name:database", eg)
		}
		name, db := parts[0], parts[1]

		if !egNamePattern.MatchString(name) {
			return nil, fmt.Errorf("invalid entity group name %q; must match [A-Za-z][A-Za-z0-9_]*", name)
		}
		if !dbNamePattern.MatchString(db) {
			return nil, fmt.Errorf("invalid database name %q; must match [A-Za-z0-9_-]+", db)
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate entity group name %q", name)
		}
		seen[name] = true
		groups = append(groups, entityGroup{name: name, database: db})
	}

	return &ValidatedSyncOptions{
		validatedSyncOptions: &validatedSyncOptions{
			RawSyncOptions:  o,
			cloudConfig:     cloudConfig,
			entityGroups:    groups,
			discoveryConfig: discoveryConfig,
		},
	}, nil
}

// Complete performs final initialization (creates Azure clients).
func (o *ValidatedSyncOptions) Complete(ctx context.Context) (*CompletedSyncOptions, error) {
	cred, err := cmdutils.GetAzureTokenCredentialsForCloud(o.cloudConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain Azure credentials: %w", err)
	}

	clientOpts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{Cloud: o.cloudConfig},
	}

	discoveryClient, err := kustoazure.NewResourceGraphKustoDiscoveryClient(cred, clientOpts, o.discoveryConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Resource Graph discovery client: %w", err)
	}

	allClusters, err := discoveryClient.DiscoverKustoClusters(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to discover Kusto clusters: %w", err)
	}

	clusters, err := selectClustersForEnvironment(allClusters, o.Environment, o.discoveryConfig.ScopeTagKey)
	if err != nil {
		return nil, err
	}

	if len(clusters) == 0 {
		scope := ""
		if o.Environment != "" {
			scope = fmt.Sprintf(" and %s=%q", o.discoveryConfig.ScopeTagKey, o.Environment)
		}
		selector := fmt.Sprintf("%s tag", o.discoveryConfig.TagKey)
		if o.discoveryConfig.TagValue != "" {
			selector = fmt.Sprintf("%s=%q tag", o.discoveryConfig.TagKey, o.discoveryConfig.TagValue)
		}
		return nil, fmt.Errorf("no Kusto clusters found with %s%s; verify the identity has Reader access and clusters are tagged", selector, scope)
	}

	return &CompletedSyncOptions{
		validatedSyncOptions: o.validatedSyncOptions,
		cred:                 cred,
		clusters:             clusters,
	}, nil
}

// selectClustersForEnvironment scopes discovered clusters to a single ARO-HCP
// environment. When environment is empty, all clusters are returned unchanged
// (legacy cross-environment behavior). When environment is set, it fails closed
// if any discovered cluster is missing a valid scope tag (scopeTagKey, by
// default aroHCPEnvironment), then returns only the clusters whose tag matches
// case-insensitively.
//
// Failing closed on a missing or invalid tag is deliberate. Discovery finds
// clusters by the selection tag (by default aroHCPPurpose) across every
// environment, and a cluster's scope tag may still be propagating (an
// in-progress rollout) or not
// yet indexed by Resource Graph. Rebuilding entity-group membership from a
// partial set would silently drop the not-yet-visible clusters and leave stale
// cross-environment groups behind, so the sync refuses to run until the whole
// fleet is consistently tagged.
func selectClustersForEnvironment(clusters []kustoazure.KustoCluster, environment, scopeTagKey string) ([]kustoazure.KustoCluster, error) {
	if environment == "" {
		return clusters, nil
	}

	var untagged []string
	for _, c := range clusters {
		if c.Environment == "" || !envPattern.MatchString(c.Environment) {
			untagged = append(untagged, c.Name)
		}
	}
	if len(untagged) > 0 {
		sort.Strings(untagged)
		return nil, fmt.Errorf("refusing to sync environment %q: %d discovered cluster(s) are missing a valid %s tag (%s); this indicates an incomplete tag rollout or Resource Graph indexing lag, so syncing now could rebuild entity groups from a partial cluster set; retry once every discovered cluster is tagged and indexed", environment, len(untagged), scopeTagKey, strings.Join(untagged, ", "))
	}

	var selected []kustoazure.KustoCluster
	for _, c := range clusters {
		if strings.EqualFold(c.Environment, environment) {
			selected = append(selected, c)
		}
	}
	return selected, nil
}

// Run executes the full chain: validate, complete, execute.
func (o *RawSyncOptions) Run(ctx context.Context) error {
	validated, err := o.Validate(ctx)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return fmt.Errorf("completion failed: %w", err)
	}

	if o.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}

	return completed.execute(ctx)
}

func (o *CompletedSyncOptions) execute(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	clusters := o.clusters

	sort.Slice(clusters, func(i, j int) bool {
		return clusters[i].Name < clusters[j].Name
	})

	logger.Info("syncing entity groups", "clusters", len(clusters), "entityGroups", len(o.entityGroups))

	// Validate all discovered URIs are valid https URLs
	for _, cluster := range clusters {
		parsed, err := url.Parse(cluster.URI)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return fmt.Errorf("discovered cluster %s has invalid URI %q; expected a valid https URL", cluster.Name, cluster.URI)
		}
	}

	// Build all KQL once per entity group (the statement is the same for every
	// target cluster since the entity group includes all clusters).
	type builtGroup struct {
		eg      entityGroup
		kqlStmt string
	}
	var built []builtGroup
	for _, eg := range o.entityGroups {
		stmt, err := buildEntityGroupKQL(eg.name, eg.database, clusters)
		if err != nil {
			return fmt.Errorf("building entity group %s/%s: %w", eg.name, eg.database, err)
		}
		logger.V(1).Info("built entity group KQL", "name", eg.name, "database", eg.database, "clusters", len(clusters), "kql", stmt)
		built = append(built, builtGroup{eg: eg, kqlStmt: stmt})
	}

	// Execute on every cluster, collecting errors so one cluster's failure
	// does not prevent reconciling the others.
	// One client per cluster, reused for all entity groups on that cluster.
	const perClusterTimeout = 30 * time.Second
	var succeeded []string
	var errs []error
	for _, cluster := range clusters {
		clusterCtx, cancel := context.WithTimeout(ctx, perClusterTimeout)

		client, err := newKustoClient(cluster.URI, o.cred)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: creating client: %w", cluster.Name, err))
			cancel()
			continue
		}

		clusterFailed := false
		for _, bg := range built {
			logger.Info("syncing", "cluster", cluster.Name, "entityGroup", bg.eg.name, "database", bg.eg.database)
			if err := executeKQL(clusterCtx, client, bg.eg.database, bg.kqlStmt); err != nil {
				errs = append(errs, fmt.Errorf("%s/%s on %s: %w", bg.eg.name, bg.eg.database, cluster.Name, err))
				clusterFailed = true
				break
			}
		}

		_ = client.Close()
		cancel()

		if !clusterFailed {
			succeeded = append(succeeded, cluster.Name)
			logger.Info("cluster sync complete", "cluster", cluster.Name, "entityGroups", len(built))
		}
	}

	logger.Info("entity group sync finished", "clusters", len(clusters), "succeeded", len(succeeded), "failed", len(errs))

	if len(errs) > 0 {
		return fmt.Errorf("sync completed with %d error(s) (succeeded: %v):\n%w", len(errs), succeeded, errors.Join(errs...))
	}
	return nil
}

func buildEntityGroupKQL(entityGroupName, database string, clusters []kustoazure.KustoCluster) (string, error) {
	if len(clusters) == 0 {
		return "", fmt.Errorf("no clusters to include in entity group %s", entityGroupName)
	}

	var b strings.Builder
	fmt.Fprintf(&b, ".create-or-alter entity_group %s (\n", entityGroupName)
	for i, cluster := range clusters {
		separator := ","
		if i == len(clusters)-1 {
			separator = ""
		}
		fmt.Fprintf(&b, "    cluster('%s').database('%s')%s\n", cluster.URI, database, separator)
	}
	b.WriteString(")")
	return b.String(), nil
}

func newKustoClient(clusterURI string, cred azcore.TokenCredential) (*azkustodata.Client, error) {
	kcsb := azkustodata.NewConnectionStringBuilder(clusterURI).WithTokenCredential(cred)
	client, err := azkustodata.New(kcsb)
	if err != nil {
		return nil, fmt.Errorf("creating Kusto client for %s: %w", clusterURI, err)
	}
	return client, nil
}

func executeKQL(ctx context.Context, client *azkustodata.Client, database, kqlStatement string) error {
	stmt := (&kql.Builder{}).AddUnsafe(kqlStatement)
	_, err := client.Mgmt(ctx, database, stmt)
	if err != nil {
		return fmt.Errorf("executing management command: %w", err)
	}
	return nil
}
