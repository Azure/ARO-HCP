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
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/agent"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	snapshotpkg "github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

// Supported --provider values.
const (
	providerCopilot    = "copilot"
	providerClaude     = "claude"
	providerClaudeCode = "claude-code"
)

// Default Kusto database names used in dynamic mode.
const (
	defaultServiceDatabase          = "ServiceLogs"
	defaultHCPDatabase              = "HostedControlPlaneLogs"
	defaultMonitoringEventsDatabase = "MonitoringEvents"
)

// RawAnalyzeOptions holds the unvalidated CLI options for the analyze subcommand.
type RawAnalyzeOptions struct {
	DataDir             string
	AROHCPPath          string
	HypershiftPath      string
	MaestroPath         string
	ClustersServicePath string
	ReviewRounds        int
	MaxRounds           int
	Output              string
	Model               string

	// Intent/IntentFile provide a free-form investigation objective (e.g. a
	// customer complaint). When set, the analysis runs in intent mode instead
	// of the default failed-test mode. They are mutually exclusive.
	Intent     string
	IntentFile string

	// Dynamic/DynamicFile provide the objective of a dynamic investigation,
	// which runs without a snapshot: the agent queries Kusto directly, guided
	// by the query library. They are mutually exclusive with each other and
	// with Intent/IntentFile.
	Dynamic     string
	DynamicFile string

	// Kusto/Region and the database names select the Kusto cluster queried in
	// dynamic mode. Snapshot modes take them from the manifest instead.
	Kusto                    string
	Region                   string
	ServiceDatabase          string
	HCPDatabase              string
	MonitoringEventsDatabase string

	// ViaKusto/ViaRegion optionally route the kusto_query tool through a
	// reachable ADX cluster instead of connecting to the manifest's cluster
	// directly. Queries still target the manifest's cluster via cross-cluster
	// cluster() references (which the agent is instructed to always use).
	ViaKusto  string
	ViaRegion string

	// Provider selects the LLM backend: "copilot" (default), "claude", or "claude-code".
	Provider string

	// Copilot-specific options.
	AuthMode        string
	GitHubTokenFile string
	ModelEndpoint   string
	ModelDeployment string

	// Claude-specific options.
	AnthropicAPIKeyFile string
	AnthropicModel      string
	ClaudeBackend       string
	VertexProject       string
	VertexRegion        string

	Verbosity int
}

func defaultAnalyzeOptions() *RawAnalyzeOptions {
	return &RawAnalyzeOptions{
		ReviewRounds:             3,
		MaxRounds:                50,
		Provider:                 providerCopilot,
		AuthMode:                 agent.CopilotAuthModeLoggedIn,
		ServiceDatabase:          defaultServiceDatabase,
		HCPDatabase:              defaultHCPDatabase,
		MonitoringEventsDatabase: defaultMonitoringEventsDatabase,
	}
}

func bindAnalyzeOptions(opts *RawAnalyzeOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.AROHCPPath, "aro-hcp", opts.AROHCPPath, "Path to ARO-HCP git checkout (required)")
	cmd.Flags().StringVar(&opts.HypershiftPath, "hypershift", opts.HypershiftPath, "Path to HyperShift git checkout (required)")
	cmd.Flags().StringVar(&opts.MaestroPath, "maestro", opts.MaestroPath, "Path to Maestro git checkout (required)")
	cmd.Flags().StringVar(&opts.ClustersServicePath, "clusters-service", opts.ClustersServicePath, "Path to Clusters Service git checkout (required)")
	cmd.Flags().IntVar(&opts.ReviewRounds, "review-rounds", opts.ReviewRounds, "Number of review rounds")
	cmd.Flags().IntVar(&opts.MaxRounds, "max-rounds", opts.MaxRounds, "Maximum validation rounds per cycle")
	cmd.Flags().StringVar(&opts.Output, "output", opts.Output, "Output directory for analysis results (defaults to data-dir; required with --dynamic/--dynamic-file)")
	cmd.Flags().StringVar(&opts.Model, "model", opts.Model, "Override the model (applies to all providers)")

	cmd.Flags().StringVar(&opts.Intent, "intent", opts.Intent, "Free-form investigation objective (e.g. a customer complaint). When set, analyzes the snapshot against this objective instead of assuming a failed e2e test. Mutually exclusive with --intent-file, --dynamic, and --dynamic-file.")
	cmd.Flags().StringVar(&opts.IntentFile, "intent-file", opts.IntentFile, "Path to a file containing the free-form investigation objective. Mutually exclusive with --intent, --dynamic, and --dynamic-file.")
	cmd.Flags().StringVar(&opts.Dynamic, "dynamic", opts.Dynamic, "Objective of a dynamic investigation run without a snapshot: the agent queries Kusto directly, guided by the snapshot query library. Takes no <data-dir>; requires --kusto, --region, and --output. Mutually exclusive with --dynamic-file, --intent, and --intent-file.")
	cmd.Flags().StringVar(&opts.DynamicFile, "dynamic-file", opts.DynamicFile, "Path to a file containing the objective of a dynamic investigation (see --dynamic). Mutually exclusive with --dynamic, --intent, and --intent-file.")

	cmd.Flags().StringVar(&opts.Kusto, "kusto", opts.Kusto, "Azure Data Explorer cluster name to query (dynamic mode only; required with --dynamic/--dynamic-file)")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Azure Data Explorer cluster region (dynamic mode only; required with --dynamic/--dynamic-file)")
	cmd.Flags().StringVar(&opts.ServiceDatabase, "service-database", opts.ServiceDatabase, "Kusto database for service logs (dynamic mode only)")
	cmd.Flags().StringVar(&opts.HCPDatabase, "hcp-database", opts.HCPDatabase, "Kusto database for hosted control plane logs (dynamic mode only)")
	cmd.Flags().StringVar(&opts.MonitoringEventsDatabase, "monitoring-events-database", opts.MonitoringEventsDatabase, "Kusto database for monitoring events (dynamic mode only)")

	cmd.Flags().StringVar(&opts.ViaKusto, "via-kusto", opts.ViaKusto, "Route the kusto_query tool through this reachable ADX cluster instead of connecting to the target cluster (the manifest's cluster, or --kusto in dynamic mode) directly; queries still target that cluster via cross-cluster cluster(). The connecting identity needs viewer rights on the target cluster, and the standard databases must exist on this cluster.")
	cmd.Flags().StringVar(&opts.ViaRegion, "via-region", opts.ViaRegion, "Region of --via-kusto (required when --via-kusto is set)")

	// Provider selection.
	cmd.Flags().StringVar(&opts.Provider, "provider", opts.Provider, "LLM provider: copilot (default), claude, or claude-code")

	// Copilot-specific flags.
	cmd.Flags().StringVar(&opts.AuthMode, "auth-mode", opts.AuthMode, "Copilot auth mode: logged-in, token, or byok")
	cmd.Flags().StringVar(&opts.GitHubTokenFile, "github-token-file", opts.GitHubTokenFile, "Path to file containing GitHub token (required for token auth mode)")
	cmd.Flags().StringVar(&opts.ModelEndpoint, "model-endpoint", opts.ModelEndpoint, "Azure AI Foundry endpoint URL (required for byok auth mode)")
	cmd.Flags().StringVar(&opts.ModelDeployment, "model-deployment", opts.ModelDeployment, "Model deployment name (required for byok auth mode)")

	// Claude-specific flags.
	cmd.Flags().StringVar(&opts.AnthropicAPIKeyFile, "anthropic-api-key-file", opts.AnthropicAPIKeyFile, "Path to file containing the Anthropic API key (claude or claude-code; otherwise use environment/CLI authentication)")
	cmd.Flags().StringVar(&opts.AnthropicModel, "anthropic-model", opts.AnthropicModel, "Anthropic model name (defaults to "+agent.DefaultClaudeModel+")")
	cmd.Flags().StringVar(&opts.ClaudeBackend, "claude-backend", agent.ClaudeBackendAPI, "Backend for claude or claude-code: api (direct Anthropic API, default) or vertex (Google Vertex AI)")
	cmd.Flags().StringVar(&opts.VertexProject, "vertex-project", opts.VertexProject, "GCP project ID for claude or claude-code Vertex AI (env: GOOGLE_VERTEX_PROJECT_ID)")
	cmd.Flags().StringVar(&opts.VertexRegion, "vertex-region", opts.VertexRegion, "GCP region for claude or claude-code Vertex AI (env: GOOGLE_VERTEX_REGION)")

	for _, flag := range []string{"aro-hcp", "hypershift", "maestro", "clusters-service"} {
		if err := cmd.MarkFlagRequired(flag); err != nil {
			return fmt.Errorf("failed to mark %s as required: %w", flag, err)
		}
	}
	return nil
}

type validatedAnalyzeOptions struct {
	dataDir       string
	worktreePaths map[string]string
	reviewRounds  int
	maxRounds     int
	outputDir     string
	model         string

	// intent is the resolved free-form investigation objective (from --intent,
	// --intent-file, --dynamic, or --dynamic-file). Empty means failed-test mode.
	intent string

	// dynamic selects a dynamic investigation: dataDir is empty, intent holds
	// the objective, and kustoEndpoint plus the database names select the
	// Kusto cluster to query.
	dynamic                  bool
	kustoEndpoint            *url.URL
	serviceDatabase          string
	hcpDatabase              string
	monitoringEventsDatabase string

	// viaKustoEndpoint, when non-nil, is the reachable ADX cluster to connect
	// the kusto_query tool through instead of the manifest's cluster.
	viaKustoEndpoint *url.URL

	// Provider selection — exactly one of these is non-nil.
	copilotConfig    *agent.AgentConfig
	claudeConfig     *agent.ClaudeConfig
	claudeCodeConfig *agent.ClaudeCodeConfig
}

func (o *RawAnalyzeOptions) validate() (*validatedAnalyzeOptions, error) {
	// --intent, --intent-file, --dynamic, and --dynamic-file are mutually exclusive.
	var objectiveFlags []string
	for _, f := range []struct {
		name  string
		value string
	}{
		{"--intent", o.Intent},
		{"--intent-file", o.IntentFile},
		{"--dynamic", o.Dynamic},
		{"--dynamic-file", o.DynamicFile},
	} {
		if f.value != "" {
			objectiveFlags = append(objectiveFlags, f.name)
		}
	}
	if len(objectiveFlags) > 1 {
		return nil, fmt.Errorf("--intent, --intent-file, --dynamic, and --dynamic-file are mutually exclusive, got %s", strings.Join(objectiveFlags, " and "))
	}
	dynamic := o.Dynamic != "" || o.DynamicFile != ""

	validated := &validatedAnalyzeOptions{
		dataDir:      o.DataDir,
		reviewRounds: o.ReviewRounds,
		maxRounds:    o.MaxRounds,
		outputDir:    o.Output,
		model:        o.Model,
		dynamic:      dynamic,
	}

	if dynamic {
		if o.DataDir != "" {
			return nil, fmt.Errorf("--dynamic and --dynamic-file do not take a <data-dir> argument")
		}
		if o.Output == "" {
			return nil, fmt.Errorf("--output is required with --dynamic and --dynamic-file")
		}
		if o.Kusto == "" || o.Region == "" {
			return nil, fmt.Errorf("--kusto and --region are required with --dynamic and --dynamic-file")
		}
		kustoEndpoint, err := kusto.KustoEndpoint(o.Kusto, o.Region)
		if err != nil {
			return nil, err
		}
		for _, db := range []struct{ flag, value string }{
			{"--service-database", o.ServiceDatabase},
			{"--hcp-database", o.HCPDatabase},
			{"--monitoring-events-database", o.MonitoringEventsDatabase},
		} {
			if db.value == "" {
				return nil, fmt.Errorf("%s must not be empty", db.flag)
			}
		}
		validated.kustoEndpoint = kustoEndpoint
		validated.serviceDatabase = o.ServiceDatabase
		validated.hcpDatabase = o.HCPDatabase
		validated.monitoringEventsDatabase = o.MonitoringEventsDatabase
	} else {
		if o.DataDir == "" {
			return nil, fmt.Errorf("data-dir argument is required")
		}
		if o.Kusto != "" || o.Region != "" ||
			o.ServiceDatabase != defaultServiceDatabase ||
			o.HCPDatabase != defaultHCPDatabase ||
			o.MonitoringEventsDatabase != defaultMonitoringEventsDatabase {
			return nil, fmt.Errorf("--kusto, --region, --service-database, --hcp-database, and --monitoring-events-database require --dynamic or --dynamic-file; snapshot analysis uses the manifest's Kusto settings")
		}

		// Verify data directory exists and contains a manifest.
		manifestPath := filepath.Join(o.DataDir, "manifest.json")
		if _, err := os.Stat(manifestPath); err != nil {
			return nil, fmt.Errorf("data directory %q does not contain manifest.json: %w", o.DataDir, err)
		}
		if validated.outputDir == "" {
			validated.outputDir = o.DataDir
		}
	}

	// Verify all worktree paths exist.
	worktreePaths := map[string]string{
		"ARO-HCP":          o.AROHCPPath,
		"hypershift":       o.HypershiftPath,
		"maestro":          o.MaestroPath,
		"clusters-service": o.ClustersServicePath,
	}
	for name, path := range worktreePaths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("--%s path %q: %w", name, path, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("--%s path %q is not a directory", name, path)
		}
	}
	validated.worktreePaths = worktreePaths

	// Resolve the optional free-form investigation objective.
	var err error
	if dynamic {
		validated.intent, err = resolveObjective(o.Dynamic, o.DynamicFile, "--dynamic-file")
	} else {
		validated.intent, err = resolveObjective(o.Intent, o.IntentFile, "--intent-file")
	}
	if err != nil {
		return nil, err
	}

	// Resolve an optional via-cluster to route Kusto queries through.
	if o.ViaKusto != "" {
		if o.ViaRegion == "" {
			return nil, fmt.Errorf("--via-region is required when --via-kusto is set")
		}
		endpoint, err := kusto.KustoEndpoint(o.ViaKusto, o.ViaRegion)
		if err != nil {
			return nil, err
		}
		validated.viaKustoEndpoint = endpoint
	} else if o.ViaRegion != "" {
		return nil, fmt.Errorf("--via-region requires --via-kusto")
	}

	// Validate provider-specific options.
	switch o.Provider {
	case providerCopilot, "":
		agentCfg := &agent.AgentConfig{
			AuthMode:        o.AuthMode,
			GitHubTokenFile: o.GitHubTokenFile,
			ModelEndpoint:   o.ModelEndpoint,
			ModelDeployment: o.ModelDeployment,
			Model:           o.Model,
			MaxRounds:       o.MaxRounds,
			Verbosity:       o.Verbosity,
		}
		switch o.AuthMode {
		case agent.CopilotAuthModeLoggedIn, "":
			agentCfg.AuthMode = agent.CopilotAuthModeLoggedIn
		case agent.CopilotAuthModeToken:
			if o.GitHubTokenFile == "" {
				return nil, fmt.Errorf("--github-token-file is required when --auth-mode is %q", o.AuthMode)
			}
			if _, err := os.Stat(o.GitHubTokenFile); err != nil {
				return nil, fmt.Errorf("--github-token-file %q: %w", o.GitHubTokenFile, err)
			}
		case agent.CopilotAuthModeBYOK:
			if o.ModelEndpoint == "" {
				return nil, fmt.Errorf("--model-endpoint is required when --auth-mode is %q", o.AuthMode)
			}
			if o.ModelDeployment == "" {
				return nil, fmt.Errorf("--model-deployment is required when --auth-mode is %q", o.AuthMode)
			}
			cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
				AdditionallyAllowedTenants:   []string{"*"},
				RequireAzureTokenCredentials: true,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create Azure credential for BYOK auth: %w", err)
			}
			agentCfg.AzureCredential = cred
		default:
			return nil, fmt.Errorf("unknown --auth-mode %q, must be one of: logged-in, token, byok", o.AuthMode)
		}
		validated.copilotConfig = agentCfg

	case providerClaude, providerClaudeCode:
		claudeCfg := &agent.ClaudeConfig{
			APIKeyFile: o.AnthropicAPIKeyFile,
			Model:      o.AnthropicModel,
			Backend:    o.ClaudeBackend,
		}

		switch o.ClaudeBackend {
		case agent.ClaudeBackendAPI, "":
			claudeCfg.Backend = agent.ClaudeBackendAPI
			if claudeCfg.APIKeyFile != "" {
				if _, err := os.Stat(claudeCfg.APIKeyFile); err != nil {
					return nil, fmt.Errorf("--anthropic-api-key-file %q: %w", claudeCfg.APIKeyFile, err)
				}
			} else if o.Provider == providerClaude && os.Getenv("ANTHROPIC_API_KEY") == "" {
				return nil, fmt.Errorf("--anthropic-api-key-file or ANTHROPIC_API_KEY environment variable is required when --claude-backend is %q", claudeCfg.Backend)
			}
		case agent.ClaudeBackendVertex:
			vertexProject := o.VertexProject
			if vertexProject == "" {
				vertexProject = os.Getenv("GOOGLE_VERTEX_PROJECT_ID")
			}
			if vertexProject == "" {
				return nil, fmt.Errorf("--vertex-project or GOOGLE_VERTEX_PROJECT_ID is required when --claude-backend is %q", o.ClaudeBackend)
			}
			vertexRegion := o.VertexRegion
			if vertexRegion == "" {
				vertexRegion = os.Getenv("GOOGLE_VERTEX_REGION")
			}
			if vertexRegion == "" {
				return nil, fmt.Errorf("--vertex-region or GOOGLE_VERTEX_REGION is required when --claude-backend is %q", o.ClaudeBackend)
			}
			claudeCfg.VertexProject = vertexProject
			claudeCfg.VertexRegion = vertexRegion
		default:
			return nil, fmt.Errorf("unknown --claude-backend %q, must be one of: api, vertex", o.ClaudeBackend)
		}

		if o.Provider == providerClaudeCode {
			validated.claudeCodeConfig = &agent.ClaudeCodeConfig{
				Model:         o.Model,
				APIKeyFile:    claudeCfg.APIKeyFile,
				Backend:       claudeCfg.Backend,
				VertexProject: claudeCfg.VertexProject,
				VertexRegion:  claudeCfg.VertexRegion,
			}
		} else {
			validated.claudeConfig = claudeCfg
		}

	default:
		return nil, fmt.Errorf("unknown --provider %q, must be one of: copilot, claude, claude-code", o.Provider)
	}

	return validated, nil
}

func (o *validatedAnalyzeOptions) run(ctx context.Context) (runErr error) {
	logger := logr.FromContextOrDiscard(ctx)

	// Gather the snapshot inputs. A dynamic investigation has no snapshot: it
	// only knows the Kusto cluster and databases given on the command line.
	var (
		inputs        *snapshotInputs
		kustoTarget   string
		kustoDatabase string
		err           error
	)
	if o.dynamic {
		kustoTarget = o.kustoEndpoint.String()
		kustoDatabase = o.serviceDatabase
	} else {
		inputs, err = readSnapshotInputs(logger, o.dataDir)
		if err != nil {
			return err
		}
		kustoTarget = inputs.manifest.KustoCluster
		kustoDatabase = inputs.manifest.KustoDatabase
	}

	// Create Azure credential for Kusto access.
	cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
		AdditionallyAllowedTenants:   []string{"*"},
		RequireAzureTokenCredentials: true,
	})
	if err != nil {
		return fmt.Errorf("failed to create Azure credential: %w", err)
	}

	// Create the Kusto client. The client connects to the target cluster (the
	// manifest's, or --kusto in dynamic mode) by default, but when --via-kusto
	// is set it connects to that reachable cluster instead; the agent's queries
	// still target the target cluster via cross-cluster cluster() references,
	// and hydration share links continue to point at the target cluster.
	kustoConnectURI := kustoTarget
	if o.viaKustoEndpoint != nil {
		kustoConnectURI = o.viaKustoEndpoint.String()
		logger.Info("Routing Kusto queries through via-cluster",
			"viaCluster", kustoConnectURI,
			"targetCluster", kustoTarget,
		)
	}
	kustoClient, err := agent.NewADXKustoClient(cred, kustoConnectURI, kustoDatabase)
	if err != nil {
		return fmt.Errorf("failed to create Kusto client: %w", err)
	}
	defer func() {
		if err := kustoClient.Close(); err != nil {
			logger.Error(err, "Failed to close Kusto client.")
		}
	}()
	cachedKustoClient := agent.NewCachingKustoClient(agent.NewLoggingKustoClient(kustoClient, logger))

	// Build provider-neutral tool definitions and domain-only prompt.
	// Each provider adds its own identity/tone framing, so we pass only
	// the domain content here to avoid duplicating those sections. The mode
	// selects failed-test framing (default), free-form intent framing, or a
	// dynamic investigation without a snapshot.
	mode := agent.ModeTest
	switch {
	case o.dynamic:
		mode = agent.ModeDynamic
	case o.intent != "":
		mode = agent.ModeIntent
	}
	kustoTool := agent.NewKustoToolDefinitionFor(mode, cachedKustoClient)
	domainPrompt, err := agent.BuildDomainPromptFor(mode)
	if err != nil {
		return fmt.Errorf("failed to build domain prompt: %w", err)
	}

	// Set up workspace with symlinks in a temp directory. Dynamic mode has no
	// data directory; the workspace holds the query library instead.
	workspaceDir, cleanup, err := setupAnalysisWorkspace(o.dataDir, o.worktreePaths, o.dynamic)
	if err != nil {
		return fmt.Errorf("failed to set up analysis workspace: %w", err)
	}
	defer cleanup()

	analyzeOpts := agent.AnalyzeOptions{
		Intent:              o.intent,
		DataDir:             o.dataDir,
		WorktreePaths:       o.worktreePaths,
		KustoCluster:        kustoTarget,
		KustoDatabase:       kustoDatabase,
		MaxValidationRounds: o.maxRounds,
		ReviewRounds:        o.reviewRounds,
	}
	renderTitle := ""
	if o.dynamic {
		analyzeOpts.Dynamic = &agent.DynamicContext{
			KustoCluster:             kustoTarget,
			ServiceDatabase:          o.serviceDatabase,
			HCPDatabase:              o.hcpDatabase,
			MonitoringEventsDatabase: o.monitoringEventsDatabase,
			QueryLibraryDir:          filepath.Join(workspaceDir, queryLibraryWorkspaceDir),
		}
	} else {
		analyzeOpts.Manifest = inputs.manifestData
		analyzeOpts.TestName = inputs.manifest.TestName
		analyzeOpts.TestError = inputs.testError
		analyzeOpts.TestOutput = inputs.testOutput
		analyzeOpts.SiblingTests = inputs.siblingTests
		analyzeOpts.NodeConsoleLogs = inputs.nodeConsoleLogs
		analyzeOpts.NodeConsoleLogURLs = inputs.nodeConsoleLogURLs
		renderTitle = inputs.manifest.TestName
	}

	// Create the LLM provider based on configuration.
	var provider agent.LLMProvider
	switch {
	case o.copilotConfig != nil:
		copilotClient, err := agent.NewCopilotClient(o.copilotConfig)
		if err != nil {
			return fmt.Errorf("failed to create Copilot client: %w", err)
		}
		provider = copilotClient
	case o.claudeConfig != nil:
		claudeProvider, err := agent.NewClaudeProvider(ctx, o.claudeConfig)
		if err != nil {
			return fmt.Errorf("failed to create Claude provider: %w", err)
		}
		provider = claudeProvider
	case o.claudeCodeConfig != nil:
		claudeCodeProvider, err := agent.NewClaudeCodeProvider(o.claudeCodeConfig)
		if err != nil {
			return fmt.Errorf("failed to create Claude Code provider: %w", err)
		}
		provider = claudeCodeProvider
	default:
		return fmt.Errorf("no LLM provider configured")
	}
	defer func() {
		if err := provider.Stop(); err != nil {
			logger.Error(err, "Failed to stop LLM provider.")
		}
	}()

	session, err := provider.CreateProviderSession(ctx, logger, agent.ProviderSessionConfig{
		IdentityPrompt:   agent.IdentityPromptFor(mode),
		TonePrompt:       agent.TonePrompt,
		SystemPrompt:     domainPrompt,
		Tools:            []agent.ToolDefinition{kustoTool},
		WorkingDirectory: workspaceDir,
		Model:            o.model,
	})
	if err != nil {
		return fmt.Errorf("failed to create LLM session: %w", err)
	}
	usageStartedAt := time.Now().UTC()
	defer func() {
		finalizeAnalysisSession(ctx, logger, session, o.outputDir, usageStartedAt, time.Now().UTC(), &runErr)
	}()

	// Ensure the output directory exists up front so progress checkpoints can be
	// written as the analysis proceeds.
	if err := os.MkdirAll(o.outputDir, 0o755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}
	writeOutputs := func(chain *agent.HydratedChain, markdown string) {
		analysisJSON, err := json.MarshalIndent(chain, "", "  ")
		if err != nil {
			logger.Error(err, "Failed to marshal analysis output.")
			return
		}
		if err := os.WriteFile(filepath.Join(o.outputDir, "analysis.json"), analysisJSON, 0o644); err != nil {
			logger.Error(err, "Failed to write analysis.json.")
		}
		if err := os.WriteFile(filepath.Join(o.outputDir, "analysis.md"), []byte(markdown), 0o644); err != nil {
			logger.Error(err, "Failed to write analysis.md.")
		}
	}

	// Checkpoint the latest good analysis to disk after each phase so an
	// interrupted or context-exhausted run still leaves usable output.
	analyzeOpts.OnProgress = func(u agent.ProgressUpdate) {
		logger.Info("Checkpointing analysis output.", "phase", u.Phase)
		writeOutputs(u.HydratedChain, u.Markdown)
	}
	result, err := agent.Analyze(ctx, logger, session, cachedKustoClient, analyzeOpts)
	if err != nil {
		return err
	}
	hydratedChain := result.HydratedChain

	// Write the final output (identical to the last checkpoint).
	logger.Info("Writing analysis output.")
	// Prefer the model-authored title in intent and dynamic modes; in test mode
	// keep the test name as the heading so output stays backwards compatible
	// even if the model emits a title opportunistically.
	if hydratedChain.Intent != "" && hydratedChain.Title != "" {
		renderTitle = hydratedChain.Title
	}
	writeOutputs(hydratedChain, agent.RenderMarkdown(hydratedChain, renderTitle))

	return nil
}

func finalizeAnalysisSession(ctx context.Context, logger logr.Logger, session agent.LLMSession, outputDir string, startedAt, endedAt time.Time, runErr *error) {
	usage := session.Usage()
	usage.StartedAt = startedAt
	usage.EndedAt = endedAt
	usagePath := filepath.Join(outputDir, "usage.json")
	if err := writeUsageReport(usagePath, usage); err != nil {
		if *runErr == nil {
			*runErr = err
		} else {
			logger.Error(err, "Failed to preserve LLM usage after unsuccessful analysis.", "path", usagePath)
		}
	} else if *runErr == nil {
		logger.Info("Analysis complete.",
			"outputDir", outputDir,
			"analysisJSON", filepath.Join(outputDir, "analysis.json"),
			"analysisMarkdown", filepath.Join(outputDir, "analysis.md"),
			"usageJSON", usagePath,
			"usageStartedAt", usage.StartedAt,
			"usageEndedAt", usage.EndedAt,
			"uncachedInputTokens", usage.Tokens.UncachedInputTokens,
			"cacheReadInputTokens", usage.Tokens.CacheReadInputTokens,
			"cacheWriteInputTokens", usage.Tokens.CacheWriteInputTokens,
			"outputTokens", usage.Tokens.OutputTokens,
			"totalTokens", usage.Tokens.TotalTokens,
			"llmRequests", usage.Requests,
			"additionalUnits", usage.AdditionalUnits,
			"providerReportedCosts", usage.ProviderReportedCosts,
		)
	} else {
		logger.Info("Preserved LLM usage after unsuccessful analysis.",
			"usageJSON", usagePath,
			"usageStartedAt", usage.StartedAt,
			"usageEndedAt", usage.EndedAt,
			"llmRequests", usage.Requests,
			"totalTokens", usage.Tokens.TotalTokens,
		)
	}

	session.SaveConversation(filepath.Join(outputDir, "conversation.json"))
	if *runErr == nil {
		if err := session.Delete(ctx); err != nil {
			logger.Error(err, "Failed to delete LLM session.")
		}
	} else {
		if err := session.Disconnect(); err != nil {
			logger.Error(err, "Failed to disconnect LLM session.")
		}
	}
}

func writeUsageReport(path string, usage agent.UsageReport) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create usage output directory: %w", err)
	}

	usageJSON, err := json.MarshalIndent(usage, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal usage: %w", err)
	}
	if err := os.WriteFile(path, usageJSON, 0o644); err != nil {
		return fmt.Errorf("failed to write usage.json: %w", err)
	}

	return nil
}

// queryLibraryWorkspaceDir is the analysis workspace subdirectory holding the
// snapshot query library in dynamic mode.
const queryLibraryWorkspaceDir = "queries"

// setupAnalysisWorkspace creates a temporary directory with symlinks to the
// data directory (when set) and source code worktrees. With withQueryLibrary,
// it also writes the snapshot query library to the queries/ subdirectory.
// Returns the workspace path and a cleanup function that removes it.
func setupAnalysisWorkspace(dataDir string, worktreePaths map[string]string, withQueryLibrary bool) (string, func(), error) {
	workspaceDir, err := os.MkdirTemp("", "hcpctl-analyze-*")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp workspace: %w", err)
	}

	cleanup := func() {
		os.RemoveAll(workspaceDir)
	}

	// Symlink data directory.
	if dataDir != "" {
		absDataDir, err := filepath.Abs(dataDir)
		if err != nil {
			cleanup()
			return "", nil, fmt.Errorf("failed to resolve data directory: %w", err)
		}
		if err := os.Symlink(absDataDir, filepath.Join(workspaceDir, "data")); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("failed to symlink data directory: %w", err)
		}
	}

	if withQueryLibrary {
		if err := snapshotpkg.WriteQueryLibrary(filepath.Join(workspaceDir, queryLibraryWorkspaceDir)); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("failed to write query library: %w", err)
		}
	}

	// Symlink source code worktrees.
	if len(worktreePaths) > 0 {
		srcDir := filepath.Join(workspaceDir, "src")
		if err := os.MkdirAll(srcDir, 0o755); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("failed to create src directory: %w", err)
		}
		for repo, repoPath := range worktreePaths {
			absPath, err := filepath.Abs(repoPath)
			if err != nil {
				cleanup()
				return "", nil, fmt.Errorf("failed to resolve path for %s: %w", repo, err)
			}
			if err := os.Symlink(absPath, filepath.Join(srcDir, repo)); err != nil {
				cleanup()
				return "", nil, fmt.Errorf("failed to symlink worktree for %s: %w", repo, err)
			}
		}
	}

	return workspaceDir, cleanup, nil
}

// snapshotInputs holds the gathered snapshot artifacts fed to the agent in
// test and intent modes.
type snapshotInputs struct {
	manifestData       []byte
	manifest           snapshotpkg.Manifest
	testError          string
	testOutput         string
	siblingTests       string
	nodeConsoleLogs    map[string]string
	nodeConsoleLogURLs map[string]string
}

// readSnapshotInputs reads the manifest, test logs, sibling tests, and node
// console logs from a gathered snapshot directory.
func readSnapshotInputs(logger logr.Logger, dataDir string) (*snapshotInputs, error) {
	manifestData, err := os.ReadFile(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}
	inputs := &snapshotInputs{
		manifestData:       manifestData,
		testError:          readFileOrEmpty(filepath.Join(dataDir, "test_logs", "error.log")),
		testOutput:         readFileOrEmpty(filepath.Join(dataDir, "test_logs", "output.log")),
		siblingTests:       readFileOrEmpty(filepath.Join(dataDir, "sibling_tests.json")),
		nodeConsoleLogs:    make(map[string]string),
		nodeConsoleLogURLs: make(map[string]string),
	}
	if err := json.Unmarshal(manifestData, &inputs.manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	// Load node console logs from disk (if present).
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve data directory: %w", err)
	}
	nodeBootLogsDir := filepath.Join(absDataDir, "node_boot_logs")
	for _, cl := range inputs.manifest.NodeConsoleLogs {
		fileName := filepath.Base(cl.File)
		resolved, err := filepath.EvalSymlinks(filepath.Join(absDataDir, cl.File))
		if err != nil {
			logger.Error(err, "Skipping console log: failed to resolve path", "file", cl.File)
			continue
		}
		rel, err := filepath.Rel(nodeBootLogsDir, resolved)
		if err != nil || strings.HasPrefix(rel, "..") {
			logger.Error(fmt.Errorf("path %q resolves outside node_boot_logs directory", cl.File), "Skipping console log with suspicious path")
			continue
		}
		content := readFileOrEmpty(resolved)
		if content != "" {
			inputs.nodeConsoleLogs[fileName] = content
			inputs.nodeConsoleLogURLs[fileName] = cl.ArtifactURL
		}
	}
	return inputs, nil
}

// resolveObjective returns the objective given inline (text) or in a file
// (path, read from the flag named fileFlag). At most one of the two is set.
func resolveObjective(text, path, fileFlag string) (string, error) {
	if path == "" {
		return text, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read %s %q: %w", fileFlag, path, err)
	}
	objective := strings.TrimSpace(string(data))
	if objective == "" {
		return "", fmt.Errorf("%s %q is empty", fileFlag, path)
	}
	return objective, nil
}

// readFileOrEmpty reads a file and returns its contents as a string,
// or an empty string if the file does not exist or cannot be read.
func readFileOrEmpty(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func newAnalyzeCommand() (*cobra.Command, error) {
	opts := defaultAnalyzeOptions()
	cmd := &cobra.Command{
		Use:   "analyze [<data-dir>]",
		Short: "Run LLM-driven root cause analysis on gathered diagnostic data or live Kusto data",
		Long: `Analyze a previously gathered diagnostic snapshot, or investigate an
objective directly against Kusto, using an LLM agent to produce a structured
root cause analysis.

Analysis modes:
  test     (default) Explains why the e2e test captured in <data-dir> failed.
  intent   (--intent/--intent-file) Investigates a free-form objective, such
           as a customer complaint, against the snapshot in <data-dir>.
  dynamic  (--dynamic/--dynamic-file) Investigates a free-form objective
           without any snapshot: no <data-dir> is taken and nothing is read
           from disk except the source checkouts. The agent queries the Kusto
           cluster given by --kusto/--region (databases --service-database,
           --hcp-database, --monitoring-events-database) directly, guided by
           the snapshot query library (every README and KQL template), which
           is written into its workspace. --output is required.
  --intent, --intent-file, --dynamic, and --dynamic-file are mutually
  exclusive.

Supported LLM providers (--provider flag):
  copilot  GitHub Copilot SDK (default). Requires a logged-in GitHub
           Copilot CLI session (gh auth login), a GitHub token file
           (--auth-mode token), or a BYOK Azure AI endpoint
           (--auth-mode byok).
  claude   Anthropic Claude API. Supports two backends via --claude-backend:
             api    (default) Direct Anthropic API. Requires an API key via
                    --anthropic-api-key-file or the ANTHROPIC_API_KEY env var.
             vertex Google Vertex AI. Uses Application Default Credentials
                    (ADC) for keyless auth. Requires --vertex-project and
                    --vertex-region (or their env var equivalents).
  claude-code  Claude Code CLI via an in-process MCP bridge. Requires the claude
               binary on PATH. Supports the same --claude-backend api/vertex
               selection and Vertex ADC options as claude. The api backend uses
               CLI authentication, ANTHROPIC_API_KEY, CLAUDE_CODE_OAUTH_TOKEN,
               or --anthropic-api-key-file.
               Uses --model (default: claude-sonnet-4-5-20250929).
               Runs with permissions bypassed and best-effort settings/memory
               isolation; this is not a filesystem sandbox.

In snapshot modes the agent examines the manifest, test logs, and Kusto
query results; in dynamic mode it discovers everything through Kusto. It
then iteratively refines its analysis through validation and review rounds.

The four repository flags point to local git checkouts at the commits
that were deployed when the test ran (or, in dynamic mode, during the
investigated time window). The agent uses these to read
source code for evidence in its causal chain.`,
		Example: `  # Analyze with GitHub Copilot (default)
  hcpctl snapshot analyze ./snapshot-20250101-120000/periodic-ci-.../12345/TestFoo \
    --aro-hcp ~/code/ARO-HCP \
    --hypershift ~/code/hypershift \
    --maestro ~/code/maestro \
    --clusters-service ~/code/clusters-service

  # Analyze with Anthropic Claude (direct API)
  hcpctl snapshot analyze ./data \
    --provider claude \
    --anthropic-api-key-file ~/.config/anthropic/api_key \
    --aro-hcp ~/code/ARO-HCP \
    --hypershift ~/code/hypershift \
    --maestro ~/code/maestro \
    --clusters-service ~/code/clusters-service

  # Analyze with Claude via Google Vertex AI (keyless ADC auth)
  hcpctl snapshot analyze ./data \
    --provider claude \
    --claude-backend vertex \
    --vertex-project my-gcp-project \
    --vertex-region us-east5 \
    --aro-hcp ~/code/ARO-HCP \
    --hypershift ~/code/hypershift \
    --maestro ~/code/maestro \
    --clusters-service ~/code/clusters-service

  # With custom model and output directory
  hcpctl snapshot analyze ./data \
    --aro-hcp ~/code/ARO-HCP \
    --hypershift ~/code/hypershift \
    --maestro ~/code/maestro \
    --clusters-service ~/code/clusters-service \
    --model gpt-4o \
    --output ./results

  # Dynamic investigation without a snapshot, querying Kusto directly
  hcpctl snapshot analyze \
    --dynamic "Why is nodepool np-1 of cluster my-cluster in subscription <sub-id> stuck scaling since 2025-01-01T10:00Z?" \
    --kusto my-adx-cluster --region eastus \
    --aro-hcp ~/code/ARO-HCP \
    --hypershift ~/code/hypershift \
    --maestro ~/code/maestro \
    --clusters-service ~/code/clusters-service \
    --output ./results`,
		Args:          cobra.RangeArgs(0, 1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				opts.DataDir = args[0]
			}
			verbosity, _ := cmd.Flags().GetInt("verbosity")
			opts.Verbosity = verbosity
			validated, err := opts.validate()
			if err != nil {
				return err
			}
			return validated.run(cmd.Context())
		},
	}
	if err := bindAnalyzeOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}
