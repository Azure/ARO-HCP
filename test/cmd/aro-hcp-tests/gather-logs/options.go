package gatherlogs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// RawOptions contains the raw command-line options
type RawOptions struct {
	TimingFile    string
	OutputDir     string
	Subscription  string
	TestName      string
}

// ValidatedOptions contains validated command-line options
type ValidatedOptions struct {
	TimingFile    string
	OutputDir     string
	Subscription  string
	TestName      string
}

// CompletedOptions contains fully processed options ready for execution
type CompletedOptions struct {
	TimingFile      string
	OutputDir       string
	Subscription    string
	TestName        string
	ResourceGroups  []string
	logger          logr.Logger
}

// SpecTimingMetadata represents the structure of timing files
type SpecTimingMetadata struct {
	Identifier  []string                              `yaml:"identifier" json:"identifier"`
	StartedAt   string                                `yaml:"startedAt" json:"startedAt"`
	FinishedAt  string                                `yaml:"finishedAt" json:"finishedAt"`
	Steps       []StepTimingMetadata                  `yaml:"steps,omitempty" json:"steps,omitempty"`
	Deployments map[string]map[string][]Operation     `yaml:"deployments,omitempty" json:"deployments,omitempty"`
}

// StepTimingMetadata represents individual test step timing
type StepTimingMetadata struct {
	Name       string `yaml:"name" json:"name"`
	StartedAt  string `yaml:"startedAt" json:"startedAt"`
	FinishedAt string `yaml:"finishedAt" json:"finishedAt"`
}

// Operation represents a deployment operation
type Operation struct {
	OperationType  string   `yaml:"operationType" json:"operationType"`
	StartTimestamp string   `yaml:"startTimestamp" json:"startTimestamp"`
	Duration       string   `yaml:"duration" json:"duration"`
	Resource       Resource `yaml:"resource" json:"resource"`
}

// Resource represents an Azure resource
type Resource struct {
	ResourceType  string `yaml:"resourceType" json:"resourceType"`
	ResourceGroup string `yaml:"resourceGroup" json:"resourceGroup"`
	Name          string `yaml:"name" json:"name"`
}

// DefaultOptions creates default options
func DefaultOptions() *RawOptions {
	return &RawOptions{}
}

// BindOptions binds command-line flags to options
func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVarP(&opts.TimingFile, "timing-file", "t", "", "Path to timing file created by test")
	cmd.Flags().StringVarP(&opts.OutputDir, "output", "o", "", "Output directory for gathered logs (defaults to $ARTIFACT_DIR/test-name)")
	cmd.Flags().StringVarP(&opts.Subscription, "subscription", "s", "", "Azure subscription ID (defaults to $SUBSCRIPTION env var)")
	cmd.Flags().StringVarP(&opts.TestName, "test-name", "n", "", "Name of the test for output directory")

	cmd.MarkFlagRequired("timing-file")

	return nil
}

// Validate validates the raw options
func (opts *RawOptions) Validate() (*ValidatedOptions, error) {
	validated := &ValidatedOptions{
		TimingFile:   opts.TimingFile,
		OutputDir:    opts.OutputDir,
		Subscription: opts.Subscription,
		TestName:     opts.TestName,
	}

	// Validate timing file exists
	if validated.TimingFile == "" {
		return nil, fmt.Errorf("timing-file is required")
	}
	if _, err := os.Stat(validated.TimingFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("timing file does not exist: %s", validated.TimingFile)
	}

	// Set subscription from environment if not provided
	if validated.Subscription == "" {
		if envSub := os.Getenv("SUBSCRIPTION"); envSub != "" {
			validated.Subscription = envSub
		} else {
			return nil, fmt.Errorf("subscription must be provided via --subscription flag or SUBSCRIPTION environment variable")
		}
	}

	return validated, nil
}

// Complete completes the options and extracts resource groups from timing file
func (opts *ValidatedOptions) Complete(logger logr.Logger) (*CompletedOptions, error) {
	completed := &CompletedOptions{
		TimingFile:   opts.TimingFile,
		OutputDir:    opts.OutputDir,
		Subscription: opts.Subscription,
		TestName:     opts.TestName,
		logger:       logger,
	}

	// Read and parse timing file
	data, err := os.ReadFile(completed.TimingFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read timing file: %w", err)
	}

	var timingData SpecTimingMetadata
	// Try YAML first, then JSON
	if err := yaml.Unmarshal(data, &timingData); err != nil {
		if err := json.Unmarshal(data, &timingData); err != nil {
			return nil, fmt.Errorf("failed to parse timing file as YAML or JSON: %w", err)
		}
	}

	// Extract resource groups from deployments
	resourceGroups := make(map[string]bool)
	for rgName := range timingData.Deployments {
		if rgName != "" {
			resourceGroups[rgName] = true
		}
	}

	// Convert to slice
	for rg := range resourceGroups {
		completed.ResourceGroups = append(completed.ResourceGroups, rg)
	}

	// Set default output directory if not provided
	if completed.OutputDir == "" {
		artifactDir := os.Getenv("ARTIFACT_DIR")
		if artifactDir == "" {
			return nil, fmt.Errorf("output directory must be provided via --output flag or ARTIFACT_DIR environment variable must be set")
		}

		if completed.TestName == "" {
			// Extract test name from timing file identifier
			if len(timingData.Identifier) > 0 {
				completed.TestName = strings.Join(timingData.Identifier, "-")
				// Clean up test name for use as directory name
				completed.TestName = strings.ReplaceAll(completed.TestName, " ", "-")
				completed.TestName = strings.ReplaceAll(completed.TestName, "/", "-")
			} else {
				completed.TestName = "unknown-test"
			}
		}

		completed.OutputDir = filepath.Join(artifactDir, completed.TestName)
	}

	logger.Info("Completed gather-logs options",
		"timingFile", completed.TimingFile,
		"outputDir", completed.OutputDir,
		"subscription", completed.Subscription,
		"testName", completed.TestName,
		"resourceGroups", completed.ResourceGroups)

	return completed, nil
}

// Gather executes the log gathering process
func (opts *CompletedOptions) Gather(ctx context.Context) error {
	opts.logger.Info("Starting log gathering", "resourceGroups", len(opts.ResourceGroups))

	if len(opts.ResourceGroups) == 0 {
		opts.logger.Info("No resource groups found in timing file, nothing to gather")
		return nil
	}

	// Create output directory
	if err := os.MkdirAll(opts.OutputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Run hcpctl must-gather for each resource group
	for _, rg := range opts.ResourceGroups {
		if err := opts.gatherLogsForResourceGroup(ctx, rg); err != nil {
			opts.logger.Error(err, "Failed to gather logs for resource group", "resourceGroup", rg)
			// Continue with other resource groups even if one fails
		}
	}

	opts.logger.Info("Log gathering completed", "outputDir", opts.OutputDir)
	return nil
}

// gatherLogsForResourceGroup runs hcpctl must-gather for a specific resource group
func (opts *CompletedOptions) gatherLogsForResourceGroup(ctx context.Context, resourceGroup string) error {
	opts.logger.Info("Gathering logs for resource group", "resourceGroup", resourceGroup)

	// Prepare the hcpctl command
	cmd := exec.CommandContext(ctx, "hcpctl", "must-gather", "query",
		"--resource-group", resourceGroup,
		"--subscription", opts.Subscription,
		"--output", filepath.Join(opts.OutputDir, resourceGroup))

	// Set environment variables
	cmd.Env = os.Environ()

	// Capture output for logging
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("hcpctl must-gather failed for resource group %s: %w\nOutput: %s", resourceGroup, err, string(output))
	}

	opts.logger.Info("Successfully gathered logs for resource group",
		"resourceGroup", resourceGroup,
		"output", string(output))

	return nil
}
