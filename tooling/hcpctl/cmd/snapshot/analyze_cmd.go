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
	"os"
	"path/filepath"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/agent"
	snapshotpkg "github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

// Supported --provider values.
const (
	providerCopilot = "copilot"
	providerClaude  = "claude"
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

	// Provider selects the LLM backend: "copilot" (default) or "claude".
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
		ReviewRounds: 3,
		MaxRounds:    50,
		Provider:     providerCopilot,
		AuthMode:     agent.CopilotAuthModeLoggedIn,
	}
}

func bindAnalyzeOptions(opts *RawAnalyzeOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.AROHCPPath, "aro-hcp", opts.AROHCPPath, "Path to ARO-HCP git checkout (required)")
	cmd.Flags().StringVar(&opts.HypershiftPath, "hypershift", opts.HypershiftPath, "Path to HyperShift git checkout (required)")
	cmd.Flags().StringVar(&opts.MaestroPath, "maestro", opts.MaestroPath, "Path to Maestro git checkout (required)")
	cmd.Flags().StringVar(&opts.ClustersServicePath, "clusters-service", opts.ClustersServicePath, "Path to Clusters Service git checkout (required)")
	cmd.Flags().IntVar(&opts.ReviewRounds, "review-rounds", opts.ReviewRounds, "Number of review rounds")
	cmd.Flags().IntVar(&opts.MaxRounds, "max-rounds", opts.MaxRounds, "Maximum validation rounds per cycle")
	cmd.Flags().StringVar(&opts.Output, "output", opts.Output, "Output directory for analysis results (defaults to data-dir)")
	cmd.Flags().StringVar(&opts.Model, "model", opts.Model, "Override the model (applies to all providers)")

	// Provider selection.
	cmd.Flags().StringVar(&opts.Provider, "provider", opts.Provider, "LLM provider: copilot (default) or claude")

	// Copilot-specific flags.
	cmd.Flags().StringVar(&opts.AuthMode, "auth-mode", opts.AuthMode, "Copilot auth mode: logged-in, token, or byok")
	cmd.Flags().StringVar(&opts.GitHubTokenFile, "github-token-file", opts.GitHubTokenFile, "Path to file containing GitHub token (required for token auth mode)")
	cmd.Flags().StringVar(&opts.ModelEndpoint, "model-endpoint", opts.ModelEndpoint, "Azure AI Foundry endpoint URL (required for byok auth mode)")
	cmd.Flags().StringVar(&opts.ModelDeployment, "model-deployment", opts.ModelDeployment, "Model deployment name (required for byok auth mode)")

	// Claude-specific flags.
	cmd.Flags().StringVar(&opts.AnthropicAPIKeyFile, "anthropic-api-key-file", opts.AnthropicAPIKeyFile, "Path to file containing the Anthropic API key (required for --claude-backend api)")
	cmd.Flags().StringVar(&opts.AnthropicModel, "anthropic-model", opts.AnthropicModel, "Anthropic model name (defaults to "+agent.DefaultClaudeModel+")")
	cmd.Flags().StringVar(&opts.ClaudeBackend, "claude-backend", agent.ClaudeBackendAPI, "Claude API backend: api (direct Anthropic API, default) or vertex (Google Vertex AI)")
	cmd.Flags().StringVar(&opts.VertexProject, "vertex-project", opts.VertexProject, "GCP project ID for Vertex AI (env: GOOGLE_VERTEX_PROJECT_ID)")
	cmd.Flags().StringVar(&opts.VertexRegion, "vertex-region", opts.VertexRegion, "GCP region for Vertex AI (env: GOOGLE_VERTEX_REGION)")

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

	// Provider selection — exactly one of these is non-nil.
	copilotConfig *agent.AgentConfig
	claudeConfig  *agent.ClaudeConfig
}

func (o *RawAnalyzeOptions) validate() (*validatedAnalyzeOptions, error) {
	if o.DataDir == "" {
		return nil, fmt.Errorf("data-dir argument is required")
	}

	// Verify data directory exists and contains a manifest.
	manifestPath := filepath.Join(o.DataDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		return nil, fmt.Errorf("data directory %q does not contain manifest.json: %w", o.DataDir, err)
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

	outputDir := o.Output
	if outputDir == "" {
		outputDir = o.DataDir
	}

	validated := &validatedAnalyzeOptions{
		dataDir:       o.DataDir,
		worktreePaths: worktreePaths,
		reviewRounds:  o.ReviewRounds,
		maxRounds:     o.MaxRounds,
		outputDir:     outputDir,
		model:         o.Model,
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

	case providerClaude:
		claudeCfg := &agent.ClaudeConfig{
			APIKeyFile: o.AnthropicAPIKeyFile,
			Model:      o.AnthropicModel,
			Verbosity:  o.Verbosity,
			Backend:    o.ClaudeBackend,
		}

		switch o.ClaudeBackend {
		case agent.ClaudeBackendAPI, "":
			claudeCfg.Backend = agent.ClaudeBackendAPI
			if claudeCfg.APIKeyFile != "" {
				if _, err := os.Stat(claudeCfg.APIKeyFile); err != nil {
					return nil, fmt.Errorf("--anthropic-api-key-file %q: %w", claudeCfg.APIKeyFile, err)
				}
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

		validated.claudeConfig = claudeCfg

	default:
		return nil, fmt.Errorf("unknown --provider %q, must be one of: copilot, claude", o.Provider)
	}

	return validated, nil
}

func (o *validatedAnalyzeOptions) run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	// Read manifest.
	manifestData, err := os.ReadFile(filepath.Join(o.dataDir, "manifest.json"))
	if err != nil {
		return fmt.Errorf("failed to read manifest: %w", err)
	}
	var manifest snapshotpkg.Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return fmt.Errorf("failed to parse manifest: %w", err)
	}

	testError := readFileOrEmpty(filepath.Join(o.dataDir, "test_logs", "error.log"))
	testOutput := readFileOrEmpty(filepath.Join(o.dataDir, "test_logs", "output.log"))
	siblingTests := readFileOrEmpty(filepath.Join(o.dataDir, "sibling_tests.json"))

	// Create Azure credential for Kusto access.
	cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
		AdditionallyAllowedTenants:   []string{"*"},
		RequireAzureTokenCredentials: true,
	})
	if err != nil {
		return fmt.Errorf("failed to create Azure credential: %w", err)
	}

	// Create Kusto client from manifest info.
	kustoClient, err := agent.NewADXKustoClient(cred, manifest.KustoCluster, manifest.KustoDatabase)
	if err != nil {
		return fmt.Errorf("failed to create Kusto client: %w", err)
	}
	defer func() {
		if err := kustoClient.Close(); err != nil {
			logger.Error(err, "Failed to close Kusto client.")
		}
	}()
	cachedKustoClient := agent.NewCachingKustoClient(kustoClient)

	// Build provider-neutral tool definitions and system prompt.
	kustoTool := agent.NewKustoToolDefinition(cachedKustoClient)
	systemPrompt, err := agent.BuildSystemPrompt()
	if err != nil {
		return fmt.Errorf("failed to build system prompt: %w", err)
	}

	// Set up workspace with symlinks in a temp directory.
	workspaceDir, cleanup, err := setupAnalysisWorkspace(o.dataDir, o.worktreePaths)
	if err != nil {
		return fmt.Errorf("failed to set up analysis workspace: %w", err)
	}
	defer cleanup()

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
	default:
		return fmt.Errorf("no LLM provider configured")
	}
	defer func() {
		if err := provider.Stop(); err != nil {
			logger.Error(err, "Failed to stop LLM provider.")
		}
	}()

	session, err := provider.CreateProviderSession(ctx, logger, agent.ProviderSessionConfig{
		SystemPrompt:     systemPrompt,
		Tools:            []agent.ToolDefinition{kustoTool},
		WorkingDirectory: workspaceDir,
		Model:            o.model,
	})
	if err != nil {
		return fmt.Errorf("failed to create LLM session: %w", err)
	}
	var analysisErr error
	defer func() {
		session.SaveConversation(filepath.Join(o.outputDir, "conversation.json"))
		if analysisErr == nil {
			if err := session.Delete(ctx); err != nil {
				logger.Error(err, "Failed to delete LLM session.")
			}
		} else {
			if err := session.Disconnect(); err != nil {
				logger.Error(err, "Failed to disconnect LLM session.")
			}
		}
	}()

	result, err := agent.Analyze(ctx, logger, session, cachedKustoClient, agent.AnalyzeOptions{
		Manifest:            manifestData,
		TestName:            manifest.TestName,
		TestError:           testError,
		TestOutput:          testOutput,
		SiblingTests:        siblingTests,
		DataDir:             o.dataDir,
		WorktreePaths:       o.worktreePaths,
		KustoCluster:        manifest.KustoCluster,
		KustoDatabase:       manifest.KustoDatabase,
		MaxValidationRounds: o.maxRounds,
		ReviewRounds:        o.reviewRounds,
	})
	if err != nil {
		analysisErr = err
		return analysisErr
	}
	hydratedChain := result.HydratedChain

	// Write output.
	logger.Info("Writing analysis output.")
	if err := os.MkdirAll(o.outputDir, 0o755); err != nil {
		analysisErr = fmt.Errorf("failed to create output directory: %w", err)
		return analysisErr
	}

	analysisJSON, err := json.MarshalIndent(hydratedChain, "", "  ")
	if err != nil {
		analysisErr = fmt.Errorf("failed to marshal analysis: %w", err)
		return analysisErr
	}
	if err := os.WriteFile(filepath.Join(o.outputDir, "analysis.json"), analysisJSON, 0o644); err != nil {
		analysisErr = fmt.Errorf("failed to write analysis.json: %w", err)
		return analysisErr
	}

	rendered := agent.RenderMarkdown(hydratedChain, manifest.TestName)
	if err := os.WriteFile(filepath.Join(o.outputDir, "analysis.md"), []byte(rendered), 0o644); err != nil {
		analysisErr = fmt.Errorf("failed to write analysis.md: %w", err)
		return analysisErr
	}

	logger.Info("Analysis complete.",
		"outputDir", o.outputDir,
		"analysisJSON", filepath.Join(o.outputDir, "analysis.json"),
		"analysisMarkdown", filepath.Join(o.outputDir, "analysis.md"),
	)

	return nil
}

// setupAnalysisWorkspace creates a temporary directory with symlinks to the
// data directory and source code worktrees. Returns the workspace path and
// a cleanup function that removes it.
func setupAnalysisWorkspace(dataDir string, worktreePaths map[string]string) (string, func(), error) {
	workspaceDir, err := os.MkdirTemp("", "hcpctl-analyze-*")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp workspace: %w", err)
	}

	cleanup := func() {
		os.RemoveAll(workspaceDir)
	}

	// Symlink data directory.
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("failed to resolve data directory: %w", err)
	}
	if err := os.Symlink(absDataDir, filepath.Join(workspaceDir, "data")); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("failed to symlink data directory: %w", err)
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
		Use:   "analyze <data-dir>",
		Short: "Run LLM-driven root cause analysis on gathered diagnostic data",
		Long: `Analyze a previously gathered diagnostic snapshot using an LLM agent
to produce a structured root cause analysis.

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

The agent examines the manifest, test logs, and Kusto query results,
then iteratively refines its analysis through validation and review rounds.

The four repository flags point to local git checkouts at the commits
that were deployed when the test ran. The agent uses these to read
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
    --output ./results`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.DataDir = args[0]
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
