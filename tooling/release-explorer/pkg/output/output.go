package output

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/tooling/release-explorer/pkg/client/types"
	"github.com/Azure/ARO-HCP/tooling/release-explorer/pkg/timeparse"
	"gopkg.in/yaml.v2"
)

func FormatOutput(
	deployments []*types.ReleaseDeployment,
	outputFormat string,
	useLocalTime bool,
	includeComponents bool,
) (string, error) {

	// Output based on format
	switch outputFormat {
	case "json":
		jsonBytes, err := json.MarshalIndent(deployments, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to format results: %w", err)
		}
		return string(jsonBytes), nil

	case "yaml":
		yamlBytes, err := yaml.Marshal(deployments)
		if err != nil {
			return "", fmt.Errorf("failed to format results: %w", err)
		}
		return string(yamlBytes), nil

	default:
		// Human-readable format
		var b strings.Builder
		fmt.Fprintf(&b, "Found %d deployment(s):\n\n", len(deployments))
		for i, deployment := range deployments {
			timestamp, err := time.Parse(time.RFC3339, deployment.Metadata.Timestamp)
			if err != nil {
				continue
			}

			displayTime := timestamp
			if useLocalTime {
				displayTime = timestamp.Local()
			}

			relativeTime := timeparse.FormatRelativeTime(time.Since(timestamp))
			fmt.Fprintf(&b, "%d. Deployment to %s was %s ago (%s)\n",
				i+1, deployment.Target.Environment, relativeTime, displayTime.Format("2006-01-02 15:04:05 MST"))
			fmt.Fprintf(&b, "   Release ID: %s\n", deployment.Metadata.ReleaseId.String())
			fmt.Fprintf(&b, "   Branch: %s\n", deployment.Metadata.Branch)
			if deployment.Metadata.PullRequestID > 0 {
				fmt.Fprintf(&b, "   PR: #%d\n", deployment.Metadata.PullRequestID)
			}
			if len(deployment.Target.RegionConfigs) > 0 {
				fmt.Fprintf(&b, "   Regions: %v\n", deployment.Target.RegionConfigs)
			}
			if includeComponents && len(deployment.Components) > 0 {
				fmt.Fprintf(&b, "   Components: %d\n", len(deployment.Components))
			}
			fmt.Fprintln(&b)
		}
		return b.String(), nil
	}
}
