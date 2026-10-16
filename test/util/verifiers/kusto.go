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
	"strconv"
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

type gatheredLogSources struct {
	sync.Mutex
	containers          map[string]bool
	orchestrationCounts map[string]int64
}

func (s *gatheredLogSources) record(logLine *mustgather.NormalizedLogLine) error {
	s.Lock()
	defer s.Unlock()
	if s.containers == nil {
		s.containers = make(map[string]bool)
		s.orchestrationCounts = make(map[string]int64)
	}
	s.containers[fmt.Sprintf("%s/%s", logLine.Namespace, logLine.ContainerName)] = true
	if logLine.QueryName == "detailedInfraOrchestrationLogs" {
		source, ok := logLine.Log["source"].(string)
		if !ok || source == "" {
			return fmt.Errorf("orchestration summary has invalid source: %v", logLine.Log["source"])
		}
		// The gatherer's normalization represents Kusto scalar values as strings.
		countText, ok := logLine.Log["rows"].(string)
		count, err := strconv.ParseInt(countText, 10, 64)
		if !ok || err != nil || count < 0 {
			return fmt.Errorf("orchestration summary for %q has invalid row count: %v", source, logLine.Log["rows"])
		}
		s.orchestrationCounts[source] += count
	}
	return nil
}

func (s *gatheredLogSources) verify(expectedSources []expectedLogSource) error {
	s.Lock()
	defer s.Unlock()
	missingSources := []string{}
	for _, expected := range expectedSources {
		if expected.Namespace != "" {
			key := fmt.Sprintf("%s/%s", expected.Namespace, expected.ContainerName)
			if !s.containers[key] {
				missingSources = append(missingSources, key)
			}
		} else if expected.NamespacePrefix != "" {
			found := false
			for key := range s.containers {
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

	missingOrchestrationSources := []string{}
	for _, source := range []string{"maestro", "hypershift", "acm-agent"} {
		if s.orchestrationCounts[source] == 0 {
			missingOrchestrationSources = append(missingOrchestrationSources, source)
		}
	}
	if len(missingSources) > 0 || len(missingOrchestrationSources) > 0 {
		return fmt.Errorf("missing expected log sources: %v; detailedInfraOrchestrationLogs expected at least one row per source, missing: %v; observed counts: maestro=%d, hypershift=%d, acm-agent=%d",
			missingSources, missingOrchestrationSources, s.orchestrationCounts["maestro"], s.orchestrationCounts["hypershift"], s.orchestrationCounts["acm-agent"])
	}
	return nil
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

	queryOptions := kusto.NewQueryOptions()
	queryOptions.SubscriptionId = v.config.SubscriptionID
	queryOptions.ResourceGroupName = v.config.ResourceGroup
	queryOptions.TimestampMin = time.Now().Add(-24 * time.Hour)
	queryOptions.TimestampMax = time.Now()
	queryOptions.Limit = -1

	var foundLogSources gatheredLogSources

	outputFunc := func(ctx context.Context, logLineChan chan *mustgather.NormalizedLogLine, options mustgather.RowOutputOptions) error {
		for logLine := range logLineChan {
			if err := foundLogSources.record(logLine); err != nil {
				return err
			}
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
			SummarizeOrchestrationLogs: true,
			QueryOptions:               &queryOptions,
		},
	)

	if err := gatherer.GatherLogs(ctx); err != nil {
		return fmt.Errorf("failed to gather logs: %w", err)
	}

	return foundLogSources.verify(v.config.ExpectedLogSources)
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
