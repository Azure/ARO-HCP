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

package framework

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/stretchr/testify/require"

	"sigs.k8s.io/yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/timing"
)

func TestRecordTestOperationPreservesKindsOutcomesAndNanoseconds(t *testing.T) {
	tc := &perItOrDescribeTestContext{}
	start := time.Date(2026, 10, 30, 12, 0, 0, 123456789, time.UTC)
	finish := start.Add(987654321 * time.Nanosecond)
	tc.RecordTestOperation(timing.KindDeleteCluster, "a label independent of operation kind", start, finish, nil)
	tc.RecordTestStep("Release leased identities", start, finish)
	for _, err := range []error{errors.New("HTTP 400"), context.DeadlineExceeded, errors.New("invalid parameters")} {
		tc.RecordTestOperation(timing.KindCreateNodePool, "unsuccessful attempt", start, finish, err)
	}
	encoded, err := yaml.Marshal(tc.timingMetadata)
	require.NoError(t, err)
	var metadata timing.SpecTimingMetadata
	require.NoError(t, yaml.Unmarshal(encoded, &metadata))
	require.Len(t, metadata.Steps, 5)
	require.Equal(t, timing.KindDeleteCluster, metadata.Steps[0].Kind)
	require.Equal(t, timing.OutcomeSucceeded, metadata.Steps[0].Outcome)
	require.Empty(t, metadata.Steps[1].Kind)
	require.Empty(t, metadata.Steps[1].Outcome, "generic spans must not fabricate a successful operation")
	for _, step := range metadata.Steps[2:] {
		require.Equal(t, timing.OutcomeFailed, step.Outcome)
	}
	require.Equal(t, start.Format(time.RFC3339Nano), metadata.Steps[0].StartedAt)
	require.Equal(t, finish.Format(time.RFC3339Nano), metadata.Steps[0].FinishedAt)
}

func TestCreateNodePoolValidationFailureRecordsFailedOutcomeAllAPIVersions(t *testing.T) {
	ctx, logger := context.Background(), logr.Discard()
	for _, test := range []struct {
		version string
		create  func(*perItOrDescribeTestContext) error
	}{
		{"20240610", func(tc *perItOrDescribeTestContext) error {
			return tc.CreateNodePoolFromParam20240610(ctx, logger, "rg", "managed", "cluster", NodePoolParams20240610{}, time.Second)
		}},
		{"20251223", func(tc *perItOrDescribeTestContext) error {
			return tc.CreateNodePoolFromParam20251223(ctx, logger, "rg", "managed", "cluster", NodePoolParams20251223{}, time.Second)
		}},
		{"20260630", func(tc *perItOrDescribeTestContext) error {
			return tc.CreateNodePoolFromParam20260630(ctx, logger, "rg", "managed", "cluster", NodePoolParams20260630{}, time.Second)
		}},
		{"20260901", func(tc *perItOrDescribeTestContext) error {
			return tc.CreateNodePoolFromParam20260901(ctx, logger, "rg", "managed", "cluster", NodePoolParams20260901{}, time.Second)
		}},
		{"20261001", func(tc *perItOrDescribeTestContext) error {
			return tc.CreateNodePoolFromParam20261001(ctx, logger, "rg", "managed", "cluster", NodePoolParams20261001{}, time.Second)
		}},
	} {
		t.Run(test.version, func(t *testing.T) {
			tc := &perItOrDescribeTestContext{}
			require.ErrorContains(t, test.create(tc), "nodePoolName")
			require.Len(t, tc.timingMetadata.Steps, 1)
			require.Equal(t, timing.KindCreateNodePool, tc.timingMetadata.Steps[0].Kind)
			require.Equal(t, timing.OutcomeFailed, tc.timingMetadata.Steps[0].Outcome)
		})
	}
}

func TestTimingFinalizationUsesResolvedSubscriptionCache(t *testing.T) {
	tc := newTestContextWithStubCredential()
	tc.perBinaryInvocationTestContext.subscriptionID = "resolved-subscription"
	tc.perBinaryInvocationTestContext.sharedDir = t.TempDir()
	tc.perBinaryInvocationTestContext.artifactDir = t.TempDir()
	subscriptionID, err := tc.SubscriptionID(context.Background())
	require.NoError(t, err)
	require.Equal(t, subscriptionID, tc.subscriptionID)
	tc.perBinaryInvocationTestContext.azureCredentials = nil
	tc.perBinaryInvocationTestContext.subscriptionID = ""
	tc.commitTimingMetadata()
	metadata := readTimingArtifact(t, tc.perBinaryInvocationTestContext.artifactDir)
	require.Equal(t, "resolved-subscription", metadata.SubscriptionID)
}

func TestTimingPhasesRetainInterruptedBoundaries(t *testing.T) {
	for _, interruption := range []string{"panic", "cancellation", "still-running"} {
		t.Run(interruption, func(t *testing.T) {
			tc := &perItOrDescribeTestContext{perBinaryInvocationTestContext: &perBinaryInvocationTestContext{
				sharedDir: t.TempDir(), artifactDir: t.TempDir(),
			}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			if interruption == "still-running" {
				go func() {
					defer close(done)
					tc.runTimingPhase(ctx, "interrupted", "Interrupted cleanup", func(context.Context) {
						close(entered)
						<-release
					})
				}()
				<-entered
			} else {
				func() {
					defer func() { _ = recover() }()
					tc.runTimingPhase(ctx, "interrupted", "Interrupted cleanup", func(context.Context) {
						if interruption == "panic" {
							panic("cleanup failed")
						}
						cancel()
					})
				}()
			}
			tc.commitTimingMetadata()
			metadata := readTimingArtifact(t, tc.perBinaryInvocationTestContext.artifactDir)
			require.Len(t, metadata.Phases, 1)
			require.True(t, metadata.Phases[0].Incomplete)
			require.NotEmpty(t, metadata.Phases[0].StartedAt)
			require.NotEmpty(t, metadata.Phases[0].FinishedAt)
			if interruption == "still-running" {
				close(release)
				<-done
				require.Equal(t, metadata.Phases, tc.timingMetadata.Phases, "a late callback must not alter finalized intervals")
			}
		})
	}
}

func TestFrameworkTimingLifecycle(t *testing.T) {
	for _, scenario := range []string{"success", "expected-rejection", "body-failure", "cleanup-failure", "cleanup-timeout"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			if retained := os.Getenv("ARO_HCP_TIMING_SMOKE_ARTIFACTS"); retained != "" {
				root = filepath.Join(retained, scenario)
				require.NoError(t, os.MkdirAll(root, 0755))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFrameworkTimingLifecycleChild$", "-test.count=1")
			cmd.Env = append(os.Environ(), "ARO_HCP_TIMING_SMOKE_CHILD="+scenario, "ARO_HCP_TIMING_SMOKE_ROOT="+root)
			output, err := cmd.CombinedOutput()
			require.NoError(t, ctx.Err(), "child exceeded its deterministic deadline: %s", output)
			if scenario == "success" || scenario == "expected-rejection" {
				require.NoError(t, err, "%s", output)
			} else {
				var exitError *exec.ExitError
				require.ErrorAs(t, err, &exitError, "%s", output)
			}
			metadata := readTimingArtifact(t, root)
			wantOutcome := "passed"
			switch scenario {
			case "body-failure", "cleanup-failure":
				wantOutcome = "failed"
			case "cleanup-timeout":
				wantOutcome = "timedout"
			}
			require.Equal(t, wantOutcome, metadata.Outcome, "%s", output)
			require.Equal(t, "cached-offline-subscription", metadata.SubscriptionID)
			require.Equal(t, []string{"offline timing lifecycle", scenario}, metadata.Identifier)
			start := parseTimingTimestamp(t, metadata.StartedAt)
			cleanupStart := parseTimingTimestamp(t, metadata.CleanupStartedAt)
			finish := parseTimingTimestamp(t, metadata.FinishedAt)
			require.True(t, start.Before(cleanupStart))
			require.True(t, cleanupStart.Before(finish))
			require.GreaterOrEqual(t, cleanupStart.Sub(start), 100*time.Millisecond, "setup time before context construction must be included")
			var phaseKinds []string
			previousFinish := start
			for _, phase := range metadata.Phases {
				phaseKinds = append(phaseKinds, phase.Kind)
				phaseStart, phaseFinish := parseTimingTimestamp(t, phase.StartedAt), parseTimingTimestamp(t, phase.FinishedAt)
				require.False(t, phaseStart.Before(previousFinish), "phases must not overlap")
				require.False(t, phaseFinish.Before(phaseStart))
				require.False(t, phaseFinish.After(finish))
				previousFinish = phaseFinish
				if phase.Kind == "controlled-cleanup" {
					require.Equal(t, scenario == "cleanup-failure" || scenario == "cleanup-timeout", phase.Incomplete)
				}
			}
			require.Equal(t, []string{"test-work", "controlled-cleanup", "arm-metadata-harvest", "debug-collection", "resource-cleanup"}, phaseKinds)
			var stepNames []string
			for _, step := range metadata.Steps {
				stepNames = append(stepNames, step.Name)
				require.False(t, parseTimingTimestamp(t, step.FinishedAt).After(finish))
			}
			if scenario == "expected-rejection" {
				var operations []timing.StepTimingMetadata
				for _, step := range metadata.Steps {
					if step.Kind == timing.KindCreateNodePool {
						operations = append(operations, step)
					}
				}
				require.Len(t, operations, 2)
				require.Equal(t, timing.OutcomeFailed, operations[0].Outcome, "the expected HTTP 400 must not inherit the passing spec outcome")
				require.Equal(t, timing.OutcomeSucceeded, operations[1].Outcome)
				require.Contains(t, operations[0].Name, "rejected")
				require.Contains(t, operations[1].Name, "accepted")
			}
			require.Contains(t, stepNames, "Controlled teardown")
			require.Contains(t, stepNames, "Delete created resources")
			require.Contains(t, stepNames, "Release leased identities")
			require.Equal(t, "Create", metadata.Deployments["retained-group"]["retained-deployment"][0].OperationType, "final serialization must retain previously harvested operations")
			azureLog, err := os.ReadFile(filepath.Join(root, "azure.log"))
			require.NoError(t, err)
			require.Contains(t, string(azureLog), "teardown log remains open")
		})
	}
}

// TestFrameworkTimingLifecycleChild runs only in an isolated subprocess so expected Ginkgo failures cannot contaminate other tests.
func TestFrameworkTimingLifecycleChild(t *testing.T) {
	scenario := os.Getenv("ARO_HCP_TIMING_SMOKE_CHILD")
	if scenario == "" {
		t.Skip("invoked only by TestFrameworkTimingLifecycle")
	}
	root := os.Getenv("ARO_HCP_TIMING_SMOKE_ROOT")
	var tc *perItOrDescribeTestContext
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.Describe("offline timing lifecycle", func() {
		ginkgo.BeforeEach(func() {
			// Exercise setup that happens before NewTestContext would normally be called.
			time.Sleep(100 * time.Millisecond)
			azureLogFile, err := os.Create(filepath.Join(root, "azure.log"))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			tc = &perItOrDescribeTestContext{
				perBinaryInvocationTestContext: &perBinaryInvocationTestContext{
					artifactDir: root, sharedDir: filepath.Join(root, "shared"), skipCleanup: true,
					azureCredentials: stubTokenCredential{token: "offline-token"},
				},
				LogDirPath:     root,
				azureLogFile:   azureLogFile,
				subscriptionID: "cached-offline-subscription",
				miEnvAssigned:  true,
				timingMetadata: timing.SpecTimingMetadata{Identifier: []string{"tree-discovery-only"}},
			}
			tc.BeforeEach(context.Background())
			// Exercise the real ARM harvest against an in-memory SDK transport, never Azure.
			options := tc.perBinaryInvocationTestContext.getClientFactoryOptions()
			options.Transport = &fakeTransport{do: func(req *http.Request) (*http.Response, error) {
				if !strings.Contains(req.URL.Path, "/deployments/retained-deployment/operations") {
					return nil, errors.New("unexpected ARM request in offline timing smoke")
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"value":[{"properties":{"provisioningOperation":"Create","timestamp":"2026-10-30T12:00:00.123456789Z","duration":"PT1S"}}]}`)),
					Request:    req,
				}, nil
			}}
			tc.armResourcesClientFactory, err = armresources.NewClientFactory(tc.subscriptionID, tc.perBinaryInvocationTestContext.azureCredentials, options)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			tc.RecordKnownDeployment("retained-group", "retained-deployment")
			ginkgo.DeferCleanup(func(ctx context.Context) {
				tc.logTestEndAndCleanupStart()
				tc.runTimingPhase(ctx, "controlled-cleanup", "Controlled cleanup", func(ctx context.Context) {
					started := time.Now()
					defer func() { tc.RecordTestStep("Controlled teardown", started, time.Now()) }()
					_, err := tc.azureLogFile.WriteString("teardown log remains open\n")
					gomega.Expect(err).NotTo(gomega.HaveOccurred())
					if scenario == "cleanup-failure" {
						ginkgo.Fail("intentional cleanup failure")
					}
					if scenario == "cleanup-timeout" {
						<-ctx.Done()
					}
				})
			}, ginkgo.NodeTimeout(50*time.Millisecond), ginkgo.GracePeriod(time.Second))
		})
		ginkgo.It(scenario, func() {
			if scenario == "body-failure" {
				ginkgo.Fail("intentional body failure")
			}
			if scenario == "expected-rejection" {
				// Exercise the public framework wrapper and SDK HTTP transport, not a manually recorded span.
				options := tc.perBinaryInvocationTestContext.getHCPClientFactoryOptions()
				var rejectedPUTs, successfulPUTs int
				options.Transport = &fakeTransport{do: func(req *http.Request) (*http.Response, error) {
					if !strings.Contains(req.URL.Path, "/nodePools/") {
						return nil, errors.New("unexpected HCP request in offline timing smoke")
					}
					status, body := http.StatusOK, `{"name":"accepted","location":"uksouth","properties":{"provisioningState":"Succeeded","replicas":1}}`
					if req.Method == http.MethodPut {
						var payload hcpsdk20240610preview.NodePool
						if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
							return nil, err
						}
						if payload.Properties == nil || payload.Properties.Replicas == nil {
							return nil, errors.New("nodepool request omitted replicas")
						}
						switch *payload.Properties.Replicas {
						case 201:
							rejectedPUTs++
							status, body = http.StatusBadRequest, `{"error":{"code":"InvalidParameter","message":"replicas must not exceed 200"}}`
						case 1:
							successfulPUTs++
						default:
							return nil, errors.New("unexpected replicas in offline timing smoke")
						}
					} else if req.Method != http.MethodGet {
						return nil, errors.New("unexpected HCP method in offline timing smoke")
					}
					return &http.Response{
						StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
						Body: io.NopCloser(strings.NewReader(body)), Request: req,
					}, nil
				}}
				var err error
				tc.clientFactory20240610, err = hcpsdk20240610preview.NewClientFactory(fakeSubscriptionID, tc.perBinaryInvocationTestContext.azureCredentials, options)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				// Disable unrelated failure-bundle collection; all provisioning still uses the real wrapper.
				tc.LogDirPath = ""
				defer func() { tc.LogDirPath = root }()
				params := NodePoolParams20240610{NodePoolName: "rejected", Replicas: 201, VMSize: "Standard_D4s_v3"}
				err = tc.CreateNodePoolFromParam20240610(context.Background(), logr.Discard(), "rg", "managed", "cluster", params, time.Second)
				var responseError *azcore.ResponseError
				gomega.Expect(errors.As(err, &responseError)).To(gomega.BeTrue())
				gomega.Expect(responseError.StatusCode).To(gomega.Equal(http.StatusBadRequest))
				params.NodePoolName, params.Replicas = "accepted", 1
				gomega.Expect(tc.CreateNodePoolFromParam20240610(context.Background(), logr.Discard(), "rg", "managed", "cluster", params, time.Second)).To(gomega.Succeed())
				gomega.Expect(rejectedPUTs).To(gomega.Equal(1))
				gomega.Expect(successfulPUTs).To(gomega.Equal(1))
			}
		})
	})
	suiteConfig, reporterConfig := ginkgo.GinkgoConfiguration()
	suiteConfig.Timeout = 5 * time.Second
	reporterConfig.JSONReport = filepath.Join(root, "ginkgo-report.json")
	ginkgo.RunSpecs(t, "Offline lifecycle timing", suiteConfig, reporterConfig)
	_, err := tc.azureLogFile.WriteString("must be closed")
	if !errors.Is(err, os.ErrClosed) {
		t.Errorf("Azure log was not closed after teardown: %v", err)
	}
}

// ReadTimingArtifact reads the real serialized artifact, not the writer's in-memory state.
func readTimingArtifact(t *testing.T, root string) timing.SpecTimingMetadata {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, "test-timing", "timing-metadata-*.yaml"))
	require.NoError(t, err)
	require.Len(t, paths, 1)
	encoded, err := os.ReadFile(paths[0])
	require.NoError(t, err)
	var metadata timing.SpecTimingMetadata
	require.NoError(t, yaml.Unmarshal(encoded, &metadata))
	return metadata
}

// ParseTimingTimestamp checks the on-disk timestamp's precision and returns its instant.
func parseTimingTimestamp(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	require.NoError(t, err)
	require.True(t, strings.Contains(value, "."), "timestamp must preserve subsecond precision: %s", value)
	require.False(t, slices.Contains([]string{"0001-01-01T00:00:00Z", ""}, value))
	return parsed
}
