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

package base

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
)

// BaseOptions represents common options used across multiple commands.
type BaseOptions struct {
	SubscriptionID    string
	ResourceGroup     string
	GrafanaName       string
	GrafanaResourceID string
	OutputFormat      string
	DryRun            bool
	Timeout           time.Duration
	ARMEndpoint       string
	AADAuthority      string
	// DiscoveryTagKey is the resource tag that marks an Azure Monitor Workspace
	// as a discovery target across subscriptions. Defaults to aroHCPPurpose.
	DiscoveryTagKey string
}

// CompletedBaseOptions represents base options that have been validated and resolved
type CompletedBaseOptions struct {
	// CloudConfig is the resolved Azure SDK cloud configuration derived from the
	// ARMEndpoint and AADAuthority flags (or the public cloud defaults when not set)
	CloudConfig cloud.Configuration
}

// DefaultBaseOptions returns a new BaseOptions with default values
func DefaultBaseOptions() *BaseOptions {
	return &BaseOptions{
		OutputFormat:    "table",
		Timeout:         30 * time.Minute,
		DiscoveryTagKey: "aroHCPPurpose",
	}
}

// BindBaseOptions binds common command-line flags to the base options
func BindBaseOptions(opts *BaseOptions, cmd *cobra.Command) error {
	flags := cmd.Flags()
	flags.StringVar(&opts.SubscriptionID, "subscription", opts.SubscriptionID, "Azure subscription ID ")
	flags.StringVar(&opts.ResourceGroup, "resource-group", opts.ResourceGroup, "Azure resource group name ")
	flags.StringVar(&opts.GrafanaName, "grafana-name", opts.GrafanaName, "Azure Managed Grafana instance name ")
	flags.StringVar(&opts.GrafanaResourceID, "grafana-resource-id", opts.GrafanaResourceID, "Azure Managed Grafana instance resource ID")
	flags.StringVar(&opts.OutputFormat, "output", opts.OutputFormat, "Output format: table or json")
	flags.BoolVar(&opts.DryRun, "dry-run", opts.DryRun, "Print actions without executing them")
	flags.DurationVar(&opts.Timeout, "timeout", opts.Timeout, "Timeout for the operation")
	flags.StringVar(&opts.ARMEndpoint, "arm-endpoint", opts.ARMEndpoint, "Azure Resource Manager endpoint for the target cloud. Defaults to the public cloud when unset")
	flags.StringVar(&opts.AADAuthority, "aad-authority", opts.AADAuthority, "Microsoft Entra ID (AAD) authority for the target cloud. Defaults to the public cloud when unset")
	flags.StringVar(&opts.DiscoveryTagKey, "discovery-tag-key", opts.DiscoveryTagKey, "resource tag key that marks an Azure Monitor Workspace as a discovery target (default aroHCPPurpose)")

	return nil
}

// tagKeyPattern restricts a caller-supplied discovery tag key to a safe
// identifier charset before it is interpolated into the Resource Graph
// discovery query, closing off a KQL-injection surface.
var tagKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]*$`)

// ValidateBaseOptions performs validation on the base options
func ValidateBaseOptions(opts *BaseOptions) (*CompletedBaseOptions, error) {
	// Validate required fields

	if opts.GrafanaResourceID == "" {
		if opts.SubscriptionID == "" || opts.ResourceGroup == "" || opts.GrafanaName == "" {
			return nil, fmt.Errorf("subscription ID, resource group, and grafana name are required if grafana resource ID is not provided")
		}
	} else {
		resourceID, err := ValidateAzureResourceID(opts.GrafanaResourceID, "Microsoft.Dashboard/grafana")
		if err != nil {
			return nil, fmt.Errorf("failed to validate grafana resource ID: %w", err)
		}
		opts.SubscriptionID = resourceID.SubscriptionID
		opts.ResourceGroup = resourceID.ResourceGroupName
		opts.GrafanaName = resourceID.Name
	}

	// Validate output format
	if opts.OutputFormat != "table" && opts.OutputFormat != "json" {
		return nil, fmt.Errorf("output format must be 'table' or 'json', got: %s", opts.OutputFormat)
	}

	if !tagKeyPattern.MatchString(opts.DiscoveryTagKey) {
		return nil, fmt.Errorf("invalid discovery tag key %q; must match %s", opts.DiscoveryTagKey, tagKeyPattern.String())
	}

	cloudConfig, err := resolveCloudConfig(opts.ARMEndpoint, opts.AADAuthority)
	if err != nil {
		return nil, err
	}

	return &CompletedBaseOptions{CloudConfig: cloudConfig}, nil
}

// resolveCloudConfig builds a cloud.Configuration from the provided ARM endpoint
// and AAD authority. When both inputs are empty, the public cloud configuration is
// returned. When both are set, a custom configuration is built. Each input may be
// a hostname or a full URL (see normalizeEndpoint).
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
// URL-form value with the https scheme. EV2 central config typically stores
// endpoint DNS values as hostnames, so we prepend the scheme when it is missing
// to make the input forgiving for callers.
func normalizeEndpoint(endpoint string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", fmt.Errorf("endpoint cannot be empty")
	}

	if !strings.HasPrefix(endpoint, "https://") {
		endpoint = "https://" + endpoint
	}

	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("not a valid URL: %w", err)
	}
	if parsed.Scheme != "https" {
		return "", fmt.Errorf("endpoint must use https scheme, got %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("endpoint host cannot be empty")
	}

	return endpoint, nil
}

// ARMClientOptions returns an *arm.ClientOptions configured with the resolved
// cloud configuration, suitable for passing to Azure SDK ARM client constructors
func (c *CompletedBaseOptions) ARMClientOptions() *azcorearm.ClientOptions {
	return &azcorearm.ClientOptions{
		ClientOptions: azcore.ClientOptions{Cloud: c.CloudConfig},
	}
}

// ValidateAzureResourceID validates an Azure resource ID and ensures it's an Azure Managed Grafana resource
func ValidateAzureResourceID(resourceID string, expectedFullType string) (*azcorearm.ResourceID, error) {
	if resourceID == "" {
		return nil, fmt.Errorf("resourceID cannot be empty")
	}

	parsedID, err := azcorearm.ParseResourceID(resourceID)
	if err != nil {
		return nil, fmt.Errorf("invalid Azure resource ID format: %w", err)
	}

	if !strings.EqualFold(parsedID.ResourceType.String(), expectedFullType) {
		return nil, fmt.Errorf("invalid Azure resource type: expected '%s', got '%s'", expectedFullType, parsedID.ResourceType.String())
	}

	if parsedID.Name == "" {
		return nil, fmt.Errorf("resource name cannot be empty in resource ID")
	}

	return parsedID, nil
}
