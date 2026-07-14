package testrunner

import (
	"fmt"
	"io"
	"slices"
	"strings"

	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

type monitorMeta struct {
	ApiVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
}

// returns (skip, err)
// skip is true if no monitors exist in the manifest
// err is an error if the monitors are not found or the api version is unknown
func checkAzmonitorAndCoreOsMonitorsExist(manifest string, skipNamespaces []string) (bool, error) {
	smonSkip, err := ensureKindsExist(manifest, "ServiceMonitor", skipNamespaces)
	if err != nil {
		return false, err
	}
	pmonSkip, err := ensureKindsExist(manifest, "PodMonitor", skipNamespaces)
	if err != nil {
		return false, err
	}

	if smonSkip && pmonSkip {
		return true, nil
	}

	return false, nil
}

func ensureKindsExist(manifest string, kind string, skipNamespaces []string) (bool, error) {
	var azmonitorExists, coreOsExists bool

	decoder := utilyaml.NewYAMLToJSONDecoder(strings.NewReader(manifest))
	for {
		var m monitorMeta
		if err := decoder.Decode(&m); err != nil {
			if err == io.EOF {
				break
			}
		}
		if slices.Contains(skipNamespaces, m.Metadata.Namespace) {
			return true, nil
		}
		if m.Kind == kind {
			switch m.ApiVersion {
			case "monitoring.coreos.com/v1":
				coreOsExists = true
			case "azmonitoring.coreos.com/v1":
				azmonitorExists = true
			default:
				return false, fmt.Errorf("unknown %s api version: %s", kind, m.ApiVersion)
			}
		}
	}
	if !azmonitorExists && !coreOsExists {
		return true, nil
	}
	if !azmonitorExists {
		return false, fmt.Errorf("azmonitor %s does not exist in the manifest", kind)
	}
	if !coreOsExists {
		return false, fmt.Errorf("coreos %s does not exist in the manifest", kind)
	}
	return false, nil
}
