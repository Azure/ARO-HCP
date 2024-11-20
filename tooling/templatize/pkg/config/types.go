package config

import (
	"fmt"
	"strings"
)

type configProviderImpl struct {
	config string
	schema string
}

type Variables map[string]any

func (v Variables) GetByPath(path string) (interface{}, bool) {
	keys := strings.Split(path, ".")
	var current interface{} = v

	for _, key := range keys {
		if m, ok := current.(Variables); ok {
			current, ok = m[key]
			if !ok {
				return nil, false
			}
		} else {
			return nil, false
		}
	}

	return current, true
}

func NewVariableOverrides() VariableOverrides {
	return &variableOverrides{}
}

type VariableOverrides interface {
	GetDefaults() Variables
	GetCloudOverrides(cloud string) Variables
	GetDeployEnvOverrides(cloud, deployEnv string) Variables
	GetRegionOverrides(cloud, deployEnv, region string) Variables
	GetRegions(cloud, deployEnv string) []string
	GetSchema() string
	HasSchema() bool
	HasCloud(cloud string) bool
	HasDeployEnv(cloud, deployEnv string) bool
}

type variableOverrides struct {
	Schema   string    `yaml:"$schema"`
	Defaults Variables `yaml:"defaults"`
	// key is the cloud alias
	Overrides map[string]*struct {
		Defaults Variables `yaml:"defaults"`
		// key is the deploy env
		Overrides map[string]*struct {
			Defaults Variables `yaml:"defaults"`
			// key is the region name
			Overrides map[string]Variables `yaml:"regions"`
		} `yaml:"environments"`
	} `yaml:"clouds"`
}

func (vo *variableOverrides) GetSchema() string {
	return vo.Schema
}

func (vo *variableOverrides) HasSchema() bool {
	return vo.Schema != ""
}

func (vo *variableOverrides) GetDefaults() Variables {
	return vo.Defaults
}

func (vo *variableOverrides) HasCloud(cloud string) bool {
	_, ok := vo.Overrides[cloud]
	return ok
}

func (vo *variableOverrides) GetCloudOverrides(cloud string) Variables {
	if cloudOverride, ok := vo.Overrides[cloud]; ok {
		return cloudOverride.Defaults
	}
	return Variables{}
}

func (vo *variableOverrides) HasDeployEnv(cloud, deployEnv string) bool {
	if cloudOverride, ok := vo.Overrides[cloud]; ok {
		_, ok := cloudOverride.Overrides[deployEnv]
		return ok
	}
	return false
}

func (vo *variableOverrides) GetDeployEnvOverrides(cloud, deployEnv string) Variables {
	if cloudOverride, ok := vo.Overrides[cloud]; ok {
		if deployEnvOverride, ok := cloudOverride.Overrides[deployEnv]; ok {
			return deployEnvOverride.Defaults
		}
	}
	return Variables{}
}

func (vo *variableOverrides) GetRegions(cloud, deployEnv string) []string {
	deployEnvOverrides, err := vo.getAllDeployEnvRegionOverrides(cloud, deployEnv)
	if err != nil {
		return []string{}
	}
	regions := make([]string, 0, len(deployEnvOverrides))
	for region := range deployEnvOverrides {
		regions = append(regions, region)
	}
	return regions
}

func (vo *variableOverrides) getAllDeployEnvRegionOverrides(cloud, deployEnv string) (map[string]Variables, error) {
	if cloudOverride, ok := vo.Overrides[cloud]; ok {
		if deployEnvOverride, ok := cloudOverride.Overrides[deployEnv]; ok {
			return deployEnvOverride.Overrides, nil
		} else {
			return nil, fmt.Errorf("deploy env %s not found under cloud %s in config", deployEnv, cloud)
		}
	}
	return nil, fmt.Errorf("cloud %s not found in config", cloud)
}

func (vo *variableOverrides) GetRegionOverrides(cloud, deployEnv, region string) Variables {
	regionOverrides, err := vo.getAllDeployEnvRegionOverrides(cloud, deployEnv)
	if err != nil {
		return Variables{}
	}
	if regionOverrides, ok := regionOverrides[region]; ok {
		return regionOverrides
	} else {
		return Variables{}
	}
}
