package ev2

import (
	"fmt"

	"github.com/Azure/ARO-Tools/pkg/config"
)

//
// This package contains helper functions to extract EV2 conformant data from a config.yaml file.
//

func NewEv2ConfigReplacements() *config.ConfigReplacements {
	return config.NewConfigReplacements(
		"$location()",
		"$(regionShortName)",
		"$stamp()",
	)
}

// GetNonRegionalServiceConfigVariables returns all non-regional configuration variables of a config.yaml file.
// Non regional means: global variables + cloud overrides + deployment environment overrides - but not regional overrides.
// The variable values are formatted to contain EV2 $location(), $stamp() and $(serviceConfigVar) variables.
// This function is useful to get the variables to fill the `Settings` section of an EV2 `ServiceConfig.json“
func GetNonRegionalServiceConfigVariables(configProvider config.ConfigProvider, cloud, deployEnv string) (config.Variables, error) {
	return configProvider.GetVariables(cloud, deployEnv, "", NewEv2ConfigReplacements())
}

// GetRegionalServiceConfigVariableOverrides returns the regional overrides of a config.yaml file.
// The variable values are formatted to contain EV2 $location(), $stamp() and $(serviceConfigVar) variables.
// This function is useful to get the variables to fill the `Geographies/Regions` section of an EV2 `ServiceConfig.json`
func GetRegionalServiceConfigVariableOverrides(configProvider config.ConfigProvider, cloud, deployEnv string) (map[string]config.Variables, error) {
	regions, err := configProvider.GetRegions(cloud, deployEnv)
	if err != nil {
		return nil, err
	}
	overrides := make(map[string]config.Variables)
	for _, region := range regions {
		regionOverrides, err := configProvider.GetRegionOverrides(cloud, deployEnv, region, NewEv2ConfigReplacements())
		if err != nil {
			return nil, err
		}
		overrides[region] = regionOverrides
	}
	return overrides, nil
}

// ScopeBindingVariables retrieves and processes configuration variables for a given cloud and deployment environment.
// It uses the provided configProvider to fetch the variables, flattens them into a __VAR__ = $config(var) formatted map.
// This function is useful to get the find/replace pairs for an EV2 `ScopeBinding.json`
func ScopeBindingVariables(configProvider config.ConfigProvider, cloud, deployEnv string) (map[string]string, error) {
	vars, err := configProvider.GetVariables(cloud, deployEnv, "", NewEv2ConfigReplacements())
	if err != nil {
		return nil, err
	}
	flattened, _ := EV2Mapping(vars, NewDunderPlaceholders(), []string{})
	variables := make(map[string]string)
	for key, value := range flattened {
		variables[key] = fmt.Sprintf("$config(%s)", value)
	}
	return variables, nil
}
