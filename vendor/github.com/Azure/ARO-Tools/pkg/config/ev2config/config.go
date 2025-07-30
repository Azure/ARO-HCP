package ev2config

import (
	"fmt"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/pkg/config/types"

	_ "embed"
)

//go:embed config.yaml
var rawConfig []byte

func AllContexts() (map[string][]string, error) {
	ev2Config := config{}
	if err := yaml.Unmarshal(rawConfig, &ev2Config); err != nil {
		return nil, fmt.Errorf("failed to parse embedded Ev2 config: %w", err)
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
	ev2Config := config{}
	if err := yaml.Unmarshal(rawConfig, &ev2Config); err != nil {
		return nil, fmt.Errorf("failed to parse embedded Ev2 config: %w", err)
	}
	cfg := types.Configuration{}
	cloudCfg, hasCloud := ev2Config.Clouds[cloud]
	if !hasCloud {
		return nil, fmt.Errorf("failed to find cloud %s", cloud)
	}
	types.MergeConfiguration(cfg, cloudCfg.Defaults)
	regionCfg, hasRegion := cloudCfg.Regions[region]
	if !hasRegion {
		return nil, fmt.Errorf("failed to find region %s in cloud %s", region, cloud)
	}
	types.MergeConfiguration(cfg, regionCfg)
	return cfg, nil
}
