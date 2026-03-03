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

package generator

import (
	"bytes"
	"fmt"
	"io"

	"github.com/Azure/ARO-Tools/pipelines/topology"
)

func Markdown(topo topology.Topology, into io.WriteCloser) error {
	if _, err := into.Write([]byte(`# Pipeline Documentation

The tree of pipelines making up the ARO HCP service are documented here from the topology configuration.
[ADO Pipeline Overview](https://dev.azure.com/msazure/AzureRedHatOpenShift/_build?definitionScope=%5COneBranch%5Csdp-pipelines%5Chcp)

`)); err != nil {
		return err
	}

	for _, service := range topo.Services {
		if err := writeDetails(topo.Entrypoints, service, into, 0); err != nil {
			return err
		}
	}

	return into.Close()
}

func writeDetails(entrypoints []topology.Entrypoint, service topology.Service, into io.WriteCloser, depth int) error {
	summary := bytes.Buffer{}
	for i := 0; i < depth; i++ {
		summary.WriteString("  ")
	}
	summary.WriteString(fmt.Sprintf("- %s", service.ServiceGroup))
	summary.WriteString(fmt.Sprintf(" ([ref](https://github.com/Azure/ARO-HCP/tree/main/%s))", service.PipelinePath))
	summary.WriteString(fmt.Sprintf(": %s", service.Purpose))

	for _, entrypoint := range entrypoints {
		if entrypoint.Identifier == service.ServiceGroup {
			reference := ""
			if name, ok := entrypoint.Metadata["name"]; ok {
				reference += name
			}
			if reference != "" {
				summary.WriteString(fmt.Sprintf(" (%s)", reference))
			}
		}
	}

	summary.WriteString("\n")

	if _, err := into.Write(summary.Bytes()); err != nil {
		return fmt.Errorf("failed to write details: %w", err)
	}

	for _, child := range service.Children {
		if err := writeDetails(entrypoints, child, into, depth+1); err != nil {
			return err
		}
	}
	return nil
}
