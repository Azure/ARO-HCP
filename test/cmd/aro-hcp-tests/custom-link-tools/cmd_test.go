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

package customlinktools

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"

	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/test/util/testutil"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

func TestGeneratedHTML(t *testing.T) {
	fakeTime, err := time.Parse(time.RFC3339, "2022-03-17T19:00:00Z")
	if err != nil {
		t.Fatalf("failed to parse fake time: %v", err)
	}
	localClock = clocktesting.NewFakePassiveClock(fakeTime)

	ctx := logr.NewContext(t.Context(), testr.New(t))
	tmpdir := t.TempDir()
	opts := Options{
		completedOptions: &completedOptions{
			TimingInputDir: "../testdata/output",
			ADXClusterName: defaultADXClusterName,
			Steps: []pipeline.NodeInfo{
				{
					Identifier: pipeline.Identifier{
						ServiceGroup:  "Microsoft.Azure.ARO.HCP.Service.Infra",
						ResourceGroup: "service",
						Step:          "cluster",
					},
					Details: &pipeline.ExecutionDetails{
						ARM: &pipeline.ARMExecutionDetails{
							Operations: []pipeline.Operation{
								{
									OperationType: "Create",
									Resource: &pipeline.Resource{
										ResourceType:  "Microsoft.ContainerService/managedClusters",
										Name:          "hcp-underlay-prow-usw3j688-svc-1",
										ResourceGroup: "hcp-underlay-prow-usw3j688-svc-1",
									},
								},
							},
						},
					},
				},
				{
					Identifier: pipeline.Identifier{
						ServiceGroup:  "Microsoft.Azure.ARO.HCP.Management.Infra",
						ResourceGroup: "management",
						Step:          "cluster",
					},
					Details: &pipeline.ExecutionDetails{
						ARM: &pipeline.ARMExecutionDetails{
							Operations: []pipeline.Operation{
								{
									OperationType: "Create",
									Resource: &pipeline.Resource{
										ResourceType:  "Microsoft.ContainerService/managedClusters",
										Name:          "hcp-underlay-prow-usw3j688-mgmt-1",
										ResourceGroup: "hcp-underlay-prow-usw3j688-mgmt-1",
									},
								},
							},
						},
					},
				},
			},
			OutputDir: tmpdir,
		},
	}
	err = opts.Run(ctx)
	if err != nil {
		t.Fatalf("failed to run custom link tools: %v", err)
	}

	testutil.CompareFileWithFixture(t, filepath.Join(tmpdir, "custom-link-tools.html"), testutil.WithSuffix("custom-link-tools"))
	testutil.CompareFileWithFixture(t, filepath.Join(tmpdir, "custom-link-tools-test-table.html"), testutil.WithSuffix("custom-link-tools-test-table"))
}
