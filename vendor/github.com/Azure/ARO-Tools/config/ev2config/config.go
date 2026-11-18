package ev2config

import (
	"fmt"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/config/types"
	"github.com/Azure/ARO-Tools/tools/cmdutils"

	_ "embed"
)

//go:embed config.yaml
var rawConfig []byte

func readConfig() (config, error) {
	ev2Config := config{}
	if err := yaml.Unmarshal(rawConfig, &ev2Config); err != nil {
		return config{}, fmt.Errorf("failed to parse embedded Ev2 config: %w", err)
	}
	return ev2Config, nil
}

func AllContexts() (map[string][]string, error) {
	ev2Config, err := readConfig()
	if err != nil {
		return nil, err
	}
	contexts := map[string][]string{}
	for cloud := range ev2Config.Clouds {
		contexts[cloud] = []string{}
		for region := range ev2Config.Clouds[cloud].Regions {
			contexts[cloud] = append(contexts[cloud], region)
		}
	}
	return contexts, nil
}

func ResolveConfig(cloud, region string) (types.Configuration, error) {
	ev2Config, err := readConfig()
	if err != nil {
		return nil, err
	}
	cfg := types.Configuration{}
	cloudCfg, hasCloud := ev2Config.Clouds[cloud]
	if !hasCloud {
		return nil, fmt.Errorf("failed to find cloud %s", cloud)
	}
	cfg = types.MergeConfiguration(cfg, cloudCfg.Defaults)
	regionCfg, hasRegion := cloudCfg.Regions[region]
	if !hasRegion {
		return nil, fmt.Errorf("failed to find region %s in cloud %s", region, cloud)
	}
	cfg = types.MergeConfiguration(cfg, regionCfg)
	return cfg, nil
}

func ResolveConfigForCloud(cloud string) (types.Configuration, error) {
	ev2Config, err := readConfig()
	if err != nil {
		return nil, err
	}

	cloudCfg, hasCloud := ev2Config.Clouds[cloud]
	if !hasCloud {
		return nil, fmt.Errorf("failed to find cloud %s", cloud)
	}

	if len(cloudCfg.Regions) == 0 {
		return nil, fmt.Errorf("no regions available for cloud %s", cloud)
	}

	// Find the first region
	var firstRegion string
	for region := range cloudCfg.Regions {
		firstRegion = region
		break
	}

	return ResolveConfig(cloud, firstRegion)
}

func GetDefaultRegionForCloud(cloud cmdutils.RolloutCloud) (string, error) {
	// Handle dev cloud mapping
	actualCloud := cloud
	if actualCloud == cmdutils.RolloutCloudDev {
		actualCloud = cmdutils.RolloutCloudPublic
	}

	contexts, err := AllContexts()
	if err != nil {
		return "", fmt.Errorf("failed to get ev2 contexts: %w", err)
	}

	regions, exists := contexts[string(actualCloud)]
	if !exists {
		return "", fmt.Errorf("unsupported rollout cloud: %s", actualCloud)
	}

	if len(regions) == 0 {
		return "", fmt.Errorf("no regions available for cloud %s", actualCloud)
	}

	return regions[0], nil
}
