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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ReleaseInfo represents a discovered Helm release
type ReleaseInfo struct {
	Name      string
	Namespace string
	Chart     string
}

// WorkloadInfo represents a workload with its primary container image from both Helm and Kubernetes sources
type WorkloadInfo struct {
	Name         string `json:"name" yaml:"name"`
	Namespace    string `json:"namespace" yaml:"namespace"`
	Kind         string `json:"kind" yaml:"kind"`
	DesiredImage string `json:"desiredImage" yaml:"desiredImage"`
	CurrentImage string `json:"currentImage" yaml:"currentImage"`
}

// ComponentMetadata represents metadata for individual component releases
type ComponentMetadata struct {
	Name              string    `json:"name" yaml:"name"`
	CreationTimestamp time.Time `json:"creationTimestamp" yaml:"creationTimestamp"`
}

// ClusterMetadata represents metadata for cluster-level releases with SDP pipelines info
type ClusterMetadata struct {
	Name               string    `json:"name" yaml:"name"`
	CreationTimestamp  time.Time `json:"creationTimestamp" yaml:"creationTimestamp"`
	AroHcpGithubCommit string    `json:"aroHcpGithubCommit" yaml:"aroHcpGithubCommit"`
	SdpPipelinesCommit string    `json:"sdpPipelinesCommit,omitempty" yaml:"sdpPipelinesCommit,omitempty"`
}

// ComponentRelease represents a single component release
type ComponentRelease struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`
	Metadata        ComponentMetadata `json:"metadata" yaml:"metadata"`
	Workloads       []WorkloadInfo    `json:"workloads" yaml:"workloads"`
}

// NewComponentRelease creates a new ComponentRelease with proper metadata
func NewComponentRelease(name string, deploymentTime time.Time, workloads []WorkloadInfo) ComponentRelease {
	return ComponentRelease{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "service-status.hcm.openshift.io/v1",
			Kind:       "ComponentRelease",
		},
		Metadata: ComponentMetadata{
			Name:              name,
			CreationTimestamp: deploymentTime,
		},
		Workloads: workloads,
	}
}

// ClusterComponentRelease represents a collection of component releases
type ClusterComponentRelease struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`
	Metadata        ClusterMetadata    `json:"metadata" yaml:"metadata"`
	Components      []ComponentRelease `json:"components" yaml:"components"`
}

// NewClusterComponentRelease creates a new ClusterComponentRelease with proper metadata
func NewClusterComponentRelease(name, aroHcpCommit, sdpPipelinesCommit string, components []ComponentRelease) ClusterComponentRelease {
	return ClusterComponentRelease{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "service-status.hcm.openshift.io/v1",
			Kind:       "ClusterComponentRelease",
		},
		Metadata: ClusterMetadata{
			Name:               name,
			CreationTimestamp:  time.Now().UTC(),
			AroHcpGithubCommit: aroHcpCommit,
			SdpPipelinesCommit: sdpPipelinesCommit,
		},
		Components: components,
	}
}
