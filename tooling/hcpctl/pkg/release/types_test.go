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

package release

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/internal/testutil"
)

func TestWorkloadInfoSerialization(t *testing.T) {
	workload := WorkloadInfo{
		Name:         "test-deployment",
		Namespace:    "test-ns",
		Kind:         "Deployment",
		DesiredImage: "nginx:latest",
		CurrentImage: "nginx:v1.21",
	}

	t.Run("JSON serialization", func(t *testing.T) {
		testutil.CompareWithFixture(t, workload, testutil.WithExtension(".json"))
	})

	t.Run("YAML serialization", func(t *testing.T) {
		testutil.CompareWithFixture(t, workload, testutil.WithExtension(".yaml"))
	})
}

func TestComponentMetadataSerialization(t *testing.T) {
	metadata := ComponentMetadata{
		Name:              "test-component",
		CreationTimestamp: time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC),
	}

	t.Run("JSON serialization", func(t *testing.T) {
		testutil.CompareWithFixture(t, metadata, testutil.WithExtension(".json"))
	})

	t.Run("YAML serialization", func(t *testing.T) {
		testutil.CompareWithFixture(t, metadata, testutil.WithExtension(".yaml"))
	})
}

func TestClusterMetadataSerialization(t *testing.T) {
	tests := []struct {
		name     string
		metadata ClusterMetadata
	}{
		{
			name: "With all fields",
			metadata: ClusterMetadata{
				Name:               "cluster-component-releases",
				CreationTimestamp:  time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC),
				AroHcpGithubCommit: "abc123def456",
				SdpPipelinesCommit: "xyz789uvw012",
			},
		},
		{
			name: "With empty SdpPipelinesCommit - should be omitted",
			metadata: ClusterMetadata{
				Name:               "cluster-component-releases",
				CreationTimestamp:  time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC),
				AroHcpGithubCommit: "abc123def456",
				SdpPipelinesCommit: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Run("JSON serialization", func(t *testing.T) {
				testutil.CompareWithFixture(t, tt.metadata, testutil.WithExtension(".json"))
			})

			t.Run("YAML serialization", func(t *testing.T) {
				testutil.CompareWithFixture(t, tt.metadata, testutil.WithExtension(".yaml"))
			})
		})
	}
}

func TestComponentReleaseSerialization(t *testing.T) {
	workloads := []WorkloadInfo{
		{
			Name:         "backend-deployment",
			Namespace:    "aro-hcp",
			Kind:         "Deployment",
			DesiredImage: "myacr.azurecr.io/aro-hcp/backend:v1.2.3",
			CurrentImage: "myacr.azurecr.io/aro-hcp/backend:v1.2.3",
		},
		{
			Name:         "backend-worker",
			Namespace:    "aro-hcp",
			Kind:         "DaemonSet",
			DesiredImage: "myacr.azurecr.io/aro-hcp/worker:v1.0.0",
			CurrentImage: "myacr.azurecr.io/aro-hcp/worker:v0.9.9",
		},
	}

	component := NewComponentRelease("backend", time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC), workloads)

	t.Run("JSON serialization", func(t *testing.T) {
		testutil.CompareWithFixture(t, component, testutil.WithExtension(".json"))
	})

	t.Run("YAML serialization", func(t *testing.T) {
		testutil.CompareWithFixture(t, component, testutil.WithExtension(".yaml"))
	})
}

func TestClusterComponentReleaseSerialization(t *testing.T) {
	backendWorkloads := []WorkloadInfo{
		{
			Name:         "backend-deployment",
			Namespace:    "aro-hcp",
			Kind:         "Deployment",
			DesiredImage: "myacr.azurecr.io/aro-hcp/backend:v1.2.3",
			CurrentImage: "myacr.azurecr.io/aro-hcp/backend:v1.2.3",
		},
	}
	frontendWorkloads := []WorkloadInfo{
		{
			Name:         "frontend-deployment",
			Namespace:    "aro-hcp",
			Kind:         "Deployment",
			DesiredImage: "myacr.azurecr.io/aro-hcp/frontend:v2.1.0",
			CurrentImage: "myacr.azurecr.io/aro-hcp/frontend:v2.0.9",
		},
	}

	backendComponent := NewComponentRelease("backend", time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC), backendWorkloads)

	frontendComponent := NewComponentRelease("frontend", time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC), frontendWorkloads)

	components := []ComponentRelease{backendComponent, frontendComponent}

	cluster := NewClusterComponentRelease("cluster-component-releases", "abc123def456", "xyz789uvw012", components)
	// Override timestamp for golden fixture consistency
	cluster.Metadata.CreationTimestamp = time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

	t.Run("JSON serialization", func(t *testing.T) {
		testutil.CompareWithFixture(t, cluster, testutil.WithExtension(".json"))
	})

	t.Run("YAML serialization", func(t *testing.T) {
		testutil.CompareWithFixture(t, cluster, testutil.WithExtension(".yaml"))
	})
}

func TestNewComponentRelease(t *testing.T) {
	workloads := []WorkloadInfo{
		{
			Name:         "test-deployment",
			Namespace:    "test-ns",
			Kind:         "Deployment",
			DesiredImage: "nginx:latest",
			CurrentImage: "nginx:v1.21",
		},
		{
			Name:         "test-daemonset",
			Namespace:    "test-ns",
			Kind:         "DaemonSet",
			DesiredImage: "redis:alpine",
			CurrentImage: "redis:alpine",
		},
	}

	t.Run("Creates component release with correct metadata", func(t *testing.T) {
		component := NewComponentRelease("test-component", time.Now().UTC(), workloads)

		// Verify TypeMeta
		assert.Equal(t, "service-status.hcm.openshift.io/v1", component.APIVersion)
		assert.Equal(t, "ComponentRelease", component.Kind)

		// Verify Metadata
		assert.Equal(t, "test-component", component.Metadata.Name)
		assert.WithinDuration(t, time.Now().UTC(), component.Metadata.CreationTimestamp, time.Second)

		// Verify Workloads
		assert.Equal(t, workloads, component.Workloads)
	})

	t.Run("Handles empty values correctly", func(t *testing.T) {
		component := NewComponentRelease("", time.Now().UTC(), nil)

		assert.Equal(t, "", component.Metadata.Name)
		assert.Nil(t, component.Workloads)
		// TypeMeta should still be set correctly
		assert.Equal(t, "service-status.hcm.openshift.io/v1", component.APIVersion)
		assert.Equal(t, "ComponentRelease", component.Kind)
	})

	t.Run("Uses provided deployment timestamp", func(t *testing.T) {
		deploymentTime := time.Date(2025, 1, 15, 12, 30, 0, 0, time.UTC)
		component := NewComponentRelease("time-test", deploymentTime, []WorkloadInfo{})

		assert.Equal(t, deploymentTime, component.Metadata.CreationTimestamp)
	})
}

func TestNewClusterComponentRelease(t *testing.T) {
	workloads := []WorkloadInfo{
		{
			Name:         "test-deployment",
			Namespace:    "test-ns",
			Kind:         "Deployment",
			DesiredImage: "nginx:latest",
			CurrentImage: "nginx:v1.21",
		},
	}

	component := NewComponentRelease("test-component", time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC), workloads)

	components := []ComponentRelease{component}

	t.Run("Creates cluster release with correct metadata", func(t *testing.T) {
		cluster := NewClusterComponentRelease("test-cluster", "abc123", "xyz789", components)

		// Verify TypeMeta
		assert.Equal(t, "service-status.hcm.openshift.io/v1", cluster.APIVersion)
		assert.Equal(t, "ClusterComponentRelease", cluster.Kind)

		// Verify Metadata
		assert.Equal(t, "test-cluster", cluster.Metadata.Name)
		assert.Equal(t, "abc123", cluster.Metadata.AroHcpGithubCommit)
		assert.Equal(t, "xyz789", cluster.Metadata.SdpPipelinesCommit)
		assert.WithinDuration(t, time.Now().UTC(), cluster.Metadata.CreationTimestamp, time.Second)

		// Verify Components
		assert.Equal(t, components, cluster.Components)
	})

	t.Run("Handles empty values correctly", func(t *testing.T) {
		cluster := NewClusterComponentRelease("", "", "", nil)

		assert.Equal(t, "", cluster.Metadata.Name)
		assert.Equal(t, "", cluster.Metadata.AroHcpGithubCommit)
		assert.Equal(t, "", cluster.Metadata.SdpPipelinesCommit)
		assert.Nil(t, cluster.Components)
	})
}
