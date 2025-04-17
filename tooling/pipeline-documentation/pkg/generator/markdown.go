package generator

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/Azure/ARO-Tools/pkg/topology"
)

func Markdown(topo topology.Topology, into io.WriteCloser) error {
	if _, err := into.Write([]byte(`# Pipeline Documentation
The tree of pipelines making up the ARO HCP service are documented here from the topology configuration.

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
	if pipeline, ok := service.Metadata["pipeline"]; ok {
		summary.WriteString(fmt.Sprintf(" ([ref](https://github.com/Azure/ARO-HCP/tree/main/%s))", pipeline))
	}
	if purpose, ok := service.Metadata["purpose"]; ok {
		summary.WriteString(fmt.Sprintf(": %s", purpose))
	}
	if links, ok := formatPipelineLinks(service.Metadata); ok {
		summary.WriteString(" " + links)
	}

	for _, entrypoint := range entrypoints {
		if entrypoint.Identifier == service.ServiceGroup {
			reference := ""
			if name, ok := entrypoint.Metadata["name"]; ok {
				reference += name
			}
			if links, ok := formatPipelineLinks(entrypoint.Metadata); ok {
				reference += " " + links
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

func formatPipelineLinks(metadata map[string]string) (string, bool) {
	var pipelines []string
	for acronym, key := range map[string]string{
		"INT":  "intPipelineId",
		"STG":  "stgPipelineId",
		"PROD": "prodPipelineId",
	} {
		if link, ok := metadata[key]; ok && link != "" {
			pipelines = append(pipelines, fmt.Sprintf("[%s](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=%s)", acronym, link))
		}
	}
	if len(pipelines) > 0 {
		return fmt.Sprintf("[%s]", strings.Join(pipelines, ", ")), true
	}
	return "", false
}
