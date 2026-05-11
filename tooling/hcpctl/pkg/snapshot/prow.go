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
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/go-logr/logr"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"gopkg.in/yaml.v3"

	"github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
)

const (
	gcsBucket          = "test-platform-results"
	configPath         = "aro-hcp-write-config/artifacts/config.yaml"
	testStepPersistent = "aro-hcp-test-persistent"
	testStepLocal      = "aro-hcp-test-local"
	prKustoRegion      = "eastus2"
)

// ProwJobInfo holds the parsed information from a Prow job URL.
type ProwJobInfo struct {
	URL       string
	JobName   string
	ProwID    string
	GCSPrefix string
	IsPR      bool
}

// ProwJobConfig holds the Kusto connection info extracted from a Prow job's config.yaml.
type ProwJobConfig struct {
	Region          string
	KustoName       string
	HCPDatabase     string
	ServiceDatabase string
}

// FailedTest represents a single failed test with its metadata.
type FailedTest struct {
	Name          string
	Output        string
	Error         string
	StartTime     time.Time
	EndTime       time.Time
	ResourceGroup string // extracted from test output
}

// ParseProwURL extracts job name, Prow ID, GCS prefix, and PR status from a Prow job URL.
// Supports two formats:
//   - Periodic/postsubmit: https://prow.ci.openshift.org/view/gs/test-platform-results/logs/<job>/<prow-id>
//   - Presubmit (PR): https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/<org_repo>/<pr>/<job>/<prow-id>
func ParseProwURL(rawURL string) (*ProwJobInfo, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	var segments []string
	for _, s := range strings.Split(u.Path, "/") {
		if s != "" {
			segments = append(segments, s)
		}
	}

	for i, seg := range segments {
		if seg == "pr-logs" {
			if i+5 >= len(segments) {
				return nil, fmt.Errorf("URL path must contain pr-logs/pull/<org_repo>/<pr>/<job>/<prow-id>, got %q", u.Path)
			}
			if segments[i+1] != "pull" {
				return nil, fmt.Errorf("expected \"pull\" after \"pr-logs\" in URL path, got %q", segments[i+1])
			}
			prowID := segments[i+5]
			if _, err := strconv.ParseUint(prowID, 10, 64); err != nil {
				return nil, fmt.Errorf("prow ID %q is not a valid number", prowID)
			}
			return &ProwJobInfo{
				URL:       rawURL,
				JobName:   segments[i+4],
				ProwID:    prowID,
				GCSPrefix: strings.Join(segments[i:i+6], "/"),
				IsPR:      true,
			}, nil
		}
		if seg == "logs" {
			if i+2 >= len(segments) {
				return nil, fmt.Errorf("URL path must contain logs/<job>/<prow-id>, got %q", u.Path)
			}
			prowID := segments[i+2]
			if _, err := strconv.ParseUint(prowID, 10, 64); err != nil {
				return nil, fmt.Errorf("prow ID %q is not a valid number", prowID)
			}
			return &ProwJobInfo{
				URL:       rawURL,
				JobName:   segments[i+1],
				ProwID:    prowID,
				GCSPrefix: strings.Join(segments[i:i+3], "/"),
				IsPR:      false,
			}, nil
		}
	}

	return nil, fmt.Errorf("URL path does not contain a \"logs\" or \"pr-logs\" segment: %q", u.Path)
}

// FetchProwJobData downloads config and test results from a Prow job's GCS artifacts.
// Returns the Kusto config and all failed tests.
func FetchProwJobData(ctx context.Context, info *ProwJobInfo) (*ProwJobConfig, []FailedTest, error) {
	logger := logr.FromContextOrDiscard(ctx)

	gcsClient, err := storage.NewClient(ctx, option.WithoutAuthentication())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create GCS client: %w", err)
	}
	defer gcsClient.Close()

	// Find the artifact directory.
	artifactDir, err := findArtifactDir(ctx, gcsClient, info.JobName, info.GCSPrefix)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find artifact directory: %w", err)
	}
	logger.Info("Found artifact directory", "dir", artifactDir)

	artifactPrefix := fmt.Sprintf("%s/artifacts/%s", info.GCSPrefix, artifactDir)

	// Download and parse config.yaml.
	configGCSPath := fmt.Sprintf("%s/%s", artifactPrefix, configPath)
	configData, err := downloadObject(ctx, gcsClient, configGCSPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to download config.yaml: %w", err)
	}

	jobConfig, err := parseConfig(configData, info.IsPR)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse config.yaml: %w", err)
	}
	logger.Info("Parsed job config",
		"region", jobConfig.Region,
		"kusto", jobConfig.KustoName,
		"serviceDB", jobConfig.ServiceDatabase,
		"hcpDB", jobConfig.HCPDatabase,
	)

	// Download test results.
	testStep := testStepPersistent
	if info.IsPR {
		testStep = testStepLocal
	}
	testResultsPrefix := fmt.Sprintf("%s/%s/artifacts/extension_test_result_e2e_", artifactPrefix, testStep)
	testResultFiles, err := listObjects(ctx, gcsClient, testResultsPrefix)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list test result files: %w", err)
	}
	if len(testResultFiles) == 0 {
		return nil, nil, fmt.Errorf("no extension_test_result_e2e_*.json files found under %s", testResultsPrefix)
	}

	var allResults extensiontests.ExtensionTestResults
	for _, objPath := range testResultFiles {
		data, err := downloadObject(ctx, gcsClient, objPath)
		if err != nil {
			logger.Error(err, "Failed to download test result file, skipping", "path", objPath)
			continue
		}
		var results extensiontests.ExtensionTestResults
		if err := json.Unmarshal(data, &results); err != nil {
			logger.Error(err, "Failed to parse test result file, skipping", "path", objPath)
			continue
		}
		allResults = append(allResults, results...)
	}

	// Extract failed tests.
	var failed []FailedTest
	for _, result := range allResults {
		if result.Result != extensiontests.ResultFailed {
			continue
		}
		ft := FailedTest{
			Name:   result.Name,
			Output: result.Output,
			Error:  result.Error,
		}
		if result.StartTime != nil {
			ft.StartTime = time.Time(*result.StartTime)
		}
		if result.EndTime != nil {
			ft.EndTime = time.Time(*result.EndTime)
		}
		ft.ResourceGroup = extractResourceGroup(result.Output)
		failed = append(failed, ft)
	}

	logger.Info("Found test results", "total", len(allResults), "failed", len(failed))
	return jobConfig, failed, nil
}

// resourceGroupRegex matches log lines like:
//
//	"msg"="creating resource group" "resourceGroup"="private-keyvault-gxsj99"
var resourceGroupRegex = regexp.MustCompile(`"resourceGroup"="([^"]+)"`)

// extractResourceGroup parses the resource group name from test output logs.
// Tests log a line like: "msg"="creating resource group" "resourceGroup"="<name>"
func extractResourceGroup(output string) string {
	matches := resourceGroupRegex.FindStringSubmatch(output)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

// sourceConfig represents the fields we read from the Prow job's config.yaml.
type sourceConfig struct {
	Region string      `yaml:"region"`
	Kusto  sourceKusto `yaml:"kusto"`
}

type sourceKusto struct {
	KustoName                      string `yaml:"kustoName"`
	HostedControlPlaneLogsDatabase string `yaml:"hostedControlPlaneLogsDatabase"`
	ServiceLogsDatabase            string `yaml:"serviceLogsDatabase"`
}

func parseConfig(data []byte, isPR bool) (*ProwJobConfig, error) {
	var src sourceConfig
	if err := yaml.Unmarshal(data, &src); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	region := src.Region
	if isPR {
		region = prKustoRegion
	}

	return &ProwJobConfig{
		Region:          region,
		KustoName:       src.Kusto.KustoName,
		HCPDatabase:     src.Kusto.HostedControlPlaneLogsDatabase,
		ServiceDatabase: src.Kusto.ServiceLogsDatabase,
	}, nil
}

// findArtifactDir lists subdirectories under artifacts/ and returns the one
// whose name is a suffix of the job name. Longest match wins.
func findArtifactDir(ctx context.Context, gcsClient *storage.Client, jobName, gcsPrefix string) (string, error) {
	prefix := fmt.Sprintf("%s/artifacts/", gcsPrefix)
	it := gcsClient.Bucket(gcsBucket).Objects(ctx, &storage.Query{
		Prefix:    prefix,
		Delimiter: "/",
	})

	var bestMatch string
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return "", fmt.Errorf("failed to list objects: %w", err)
		}
		if attrs.Prefix == "" {
			continue
		}
		dir := strings.TrimPrefix(attrs.Prefix, prefix)
		dir = strings.TrimSuffix(dir, "/")
		if strings.HasSuffix(jobName, dir) {
			if len(dir) > len(bestMatch) {
				bestMatch = dir
			}
		}
	}

	if bestMatch == "" {
		return "", fmt.Errorf("no artifact directory found matching a suffix of job name %q under %s", jobName, prefix)
	}
	return bestMatch, nil
}

func listObjects(ctx context.Context, gcsClient *storage.Client, prefix string) ([]string, error) {
	it := gcsClient.Bucket(gcsBucket).Objects(ctx, &storage.Query{
		Prefix: prefix,
	})

	var objects []string
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", err)
		}
		if attrs.Name != "" {
			objects = append(objects, attrs.Name)
		}
	}
	return objects, nil
}

func downloadObject(ctx context.Context, gcsClient *storage.Client, path string) ([]byte, error) {
	reader, err := gcsClient.Bucket(gcsBucket).Object(path).NewReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open object %s: %w", path, err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read object %s: %w", path, err)
	}
	return data, nil
}

// SanitizeTestName replaces characters that are not alphanumeric, dashes, or
// underscores with underscores, producing a valid filesystem path component.
func SanitizeTestName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}
