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

package verifiers

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/mustgather"
)

// ExpectedLogSource represents a namespace/container combination that should have logs
type expectedLogSource struct {
	Namespace       string
	NamespacePrefix string
	ContainerName   string
	Database        string
}

// MustGatherVerifierConfig holds configuration for the must-gather verifier
type mustGatherVerifierConfig struct {
	KustoCluster       string
	KustoRegion        string
	SubscriptionID     string
	ResourceGroup      string
	QueryTimeout       time.Duration
	ExpectedLogSources []expectedLogSource
}

// verifyMustGatherLogsImpl implements the must-gather logs verifier
type verifyMustGatherLogsImpl struct {
	config mustGatherVerifierConfig
}

func (v verifyMustGatherLogsImpl) Name() string {
	return "VerifyMustGatherLogs"
}

func (v verifyMustGatherLogsImpl) Verify(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	endpoint, err := kusto.KustoEndpoint(v.config.KustoCluster, v.config.KustoRegion)
	if err != nil {
		return fmt.Errorf("failed to create Kusto endpoint: %w", err)
	}

	kustoClient, err := kusto.NewClient(endpoint, v.config.QueryTimeout)
	if err != nil {
		return fmt.Errorf("failed to create Kusto client: %w", err)
	}
	defer func() {
		if closeErr := kustoClient.Close(); closeErr != nil {
			logger.Error(closeErr, "Failed to close Kusto client")
		}
	}()

	queryClient := mustgather.NewQueryClientWithFileWriter(kustoClient, v.config.QueryTimeout, "", nil)

	queryOptions, err := mustgather.NewQueryOptions(
		v.config.SubscriptionID,
		v.config.ResourceGroup,
		"",                            // resourceId
		time.Now().Add(-24*time.Hour), // timestampMin
		time.Now(),                    // timestampMax
		-1,                            // limit: -1 means no truncation
	)
	if err != nil {
		return fmt.Errorf("failed to create query options: %w", err)
	}

	foundLogSources := make(map[string]bool)
	var foundMutex sync.Mutex

	outputFunc := func(ctx context.Context, logLineChan chan *mustgather.NormalizedLogLine, queryType mustgather.QueryType, options mustgather.RowOutputOptions) error {
		for logLine := range logLineChan {
			// Create a key for namespace/container combination
			key := fmt.Sprintf("%s/%s", logLine.Namespace, logLine.ContainerName)
			foundMutex.Lock()
			foundLogSources[key] = true
			foundMutex.Unlock()
		}
		return nil
	}

	gatherer := mustgather.NewGatherer(
		queryClient,
		outputFunc,
		mustgather.RowOutputOptions{},
		mustgather.GathererOptions{
			SkipHostedControlPlaneLogs: false,
			SkipKubernetesEventsLogs:   true,
			SkipSystemdLogs:            true,
			QueryOptions:               queryOptions,
		},
	)

	if err := gatherer.GatherLogs(ctx); err != nil {
		return fmt.Errorf("failed to gather logs: %w", err)
	}

	missingSources := []string{}
	for _, expected := range v.config.ExpectedLogSources {
		if expected.Namespace != "" {
			key := fmt.Sprintf("%s/%s", expected.Namespace, expected.ContainerName)
			if !foundLogSources[key] {
				missingSources = append(missingSources, key)
			}
		} else if expected.NamespacePrefix != "" {
			found := false
			for key := range foundLogSources {
				if strings.HasPrefix(key, expected.NamespacePrefix) && strings.HasSuffix(key, expected.ContainerName) {
					found = true
					break
				}
			}
			if !found {
				missingSources = append(missingSources, fmt.Sprintf("%s/%s", expected.NamespacePrefix, expected.ContainerName))
			}
		}
	}

	if len(missingSources) > 0 {
		return fmt.Errorf("missing expected log sources: %v", missingSources)
	}

	return nil
}

// VerifyMustGatherLogs creates a new must-gather logs verifier with default configuration
func VerifyMustGatherLogs(subscriptionID, rgName string) verifyMustGatherLogsImpl {
	config := mustGatherVerifierConfig{
		KustoCluster:   "hcp-dev-us-2",
		KustoRegion:    "eastus2",
		SubscriptionID: subscriptionID,
		ResourceGroup:  rgName,
		QueryTimeout:   5 * time.Minute,
		ExpectedLogSources: []expectedLogSource{
			{
				Namespace:     "aro-hcp",
				ContainerName: "aro-hcp-frontend",
				Database:      "ServiceLogs",
			},
			{
				Namespace:     "aro-hcp",
				ContainerName: "aro-hcp-backend",
				Database:      "ServiceLogs",
			},
			{
				Namespace:     "clusters-service",
				ContainerName: "clusters-service-server",
				Database:      "ServiceLogs",
			},
			{
				NamespacePrefix: "ocm-arohcp",
				Database:        "HostedControlPlaneLogs",
				ContainerName:   "kube-apiserver",
			},
		},
	}

	return verifyMustGatherLogsImpl{
		config: config,
	}
}
