package testrunner

import (
	"fmt"
	"io"
	"slices"
	"strings"

	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

type smonMeta struct {
	ApiVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
}

// returns (skip, azmonitorSmonExists && coreOsSmonExists, err)
// skip is true if no smons exist in the manifest
// azmonitorSmonExists && coreOsSmonExists is true if both smons exist in the manifest
// err is an error if the smons are not found or the api version is unknown
func checkAzmonitorAndCoreOsSmonsExists(manifest string, skipNamespaces []string) (bool, bool, error) {
	var azmonitorSmonExists, coreOsSmonExists bool

	decoder := utilyaml.NewYAMLToJSONDecoder(strings.NewReader(manifest))
	for {
		var smon smonMeta
		if err := decoder.Decode(&smon); err != nil {
			if err == io.EOF {
				break
			}
		}
		if slices.Contains(skipNamespaces, smon.Metadata.Namespace) {
			return true, false, nil
		}
		if smon.Kind == "ServiceMonitor" {
			switch smon.ApiVersion {
			case "monitoring.coreos.com/v1":
				coreOsSmonExists = true
			case "azmonitoring.coreos.com/v1":
				azmonitorSmonExists = true
			default:
				return false, false, fmt.Errorf("unknown smon api version: %s", smon.ApiVersion)
			}
		}
	}
	if !azmonitorSmonExists && !coreOsSmonExists {
		return true, false, nil
	}
	if !azmonitorSmonExists {
		return false, false, fmt.Errorf("azmonitor smon does not exist in the manifest")
	}
	if !coreOsSmonExists {
		return false, false, fmt.Errorf("coreos smon does not exist in the manifest")
	}
	return false, true, nil
}
