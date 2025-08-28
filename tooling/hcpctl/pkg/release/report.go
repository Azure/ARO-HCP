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
	"encoding/json"
	"fmt"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// OutputReports outputs the reports to stdout in the specified format
func OutputReports(reports []ComponentRelease, format string, aroHcpCommit, sdpPipelinesCommit string) error {
	output := ClusterComponentRelease{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "service-status.hcm.openshift.io/v1",
			Kind:       "ClusterComponentRelease",
		},
		Metadata: ClusterMetadata{
			Name:               "cluster-component-releases",
			CreationTimestamp:  time.Now().UTC(),
			AroHcpGithubCommit: aroHcpCommit,
			SdpPipelinesCommit: sdpPipelinesCommit,
		},
		Components: reports,
	}

	switch format {
	case "yaml":
		return outputYAML(output)
	case "json":
		return outputJSON(output)
	default:
		return fmt.Errorf("unsupported output format: %s", format)
	}
}

// outputYAML outputs the data as YAML
func outputYAML(data interface{}) error {
	yamlData, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal data to YAML: %w", err)
	}

	_, err = os.Stdout.Write(yamlData)
	if err != nil {
		return fmt.Errorf("failed to write YAML to stdout: %w", err)
	}

	return nil
}

// outputJSON outputs the data as pretty-printed JSON
func outputJSON(data interface{}) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(data); err != nil {
		return fmt.Errorf("failed to encode data to JSON: %w", err)
	}

	return nil
}
