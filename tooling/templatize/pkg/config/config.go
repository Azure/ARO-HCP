package config

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"text/template"

	"gopkg.in/yaml.v3"
)

func DefaultConfigReplacements() *ConfigReplacements {
	return NewConfigReplacements("", "", "")
}

func NewConfigReplacements(regionReplacement, regionShortReplacement, stampReplacement string) *ConfigReplacements {
	return &ConfigReplacements{
		RegionReplacement:      regionReplacement,
		RegionShortReplacement: regionShortReplacement,
		StampReplacement:       stampReplacement,
	}
}

type ConfigReplacements struct {
	RegionReplacement      string
	RegionShortReplacement string
	StampReplacement       string
}

func (c *ConfigReplacements) AsMap() map[string]interface{} {
	return map[string]interface{}{
		"ctx": map[string]interface{}{
			"region":      c.RegionReplacement,
			"regionShort": c.RegionShortReplacement,
			"stamp":       c.StampReplacement,
		},
	}
}

type ConfigProvider interface {
	Validate(cloud, deployEnv string) error
	GetVariables(cloud, deployEnv, region string, configReplacements *ConfigReplacements) (Variables, error)
	GetDeployEnvVariables(cloud, deployEnv string, configReplacements *ConfigReplacements) (Variables, error)
	GetRegions(cloud, deployEnv string) ([]string, error)
	GetRegionOverrides(cloud, deployEnv, region string, configReplacements *ConfigReplacements) (Variables, error)
}

func NewConfigProvider(config string) ConfigProvider {
	return &configProviderImpl{
		config: config,
	}
}

func InterfaceToVariables(i interface{}) (Variables, bool) {
	// Helper, that reduces need for reflection calls, i.e. MapIndex
	// from: https://github.com/peterbourgon/mergemap/blob/master/mergemap.go
	value := reflect.ValueOf(i)
	if value.Kind() == reflect.Map {
		m := Variables{}
		for _, k := range value.MapKeys() {
			v := value.MapIndex(k).Interface()
			if nestedMap, ok := InterfaceToVariables(v); ok {
				m[k.String()] = nestedMap
			} else {
				m[k.String()] = v
			}
		}
		return m, true
	}
	return Variables{}, false
}

// Merges variables, returns merged variables
// However the return value is only used for recursive updates on the map
// The actual merged variables are updated in the base map
func mergeVariables(base, override Variables) Variables {
	for k, newValue := range override {
		if baseValue, exists := base[k]; exists {
			srcMap, srcMapOk := InterfaceToVariables(newValue)
			dstMap, dstMapOk := InterfaceToVariables(baseValue)
			if srcMapOk && dstMapOk {
				newValue = mergeVariables(dstMap, srcMap)
			}
		}
		base[k] = newValue
	}

	return base
}

func (cp *configProviderImpl) GetVariables(cloud, deployEnv, region string, configReplacements *ConfigReplacements) (Variables, error) {
	variables, err := cp.GetDeployEnvVariables(cloud, deployEnv, configReplacements)
	if err != nil {
		return nil, err
	}

	// region overrides
	regionOverrides, err := cp.GetRegionOverrides(cloud, deployEnv, region, configReplacements)
	if err != nil {
		return nil, err
	}
	mergeVariables(variables, regionOverrides)

	return variables, nil
}

func (cp *configProviderImpl) Validate(cloud, deployEnv string) error {
	config, err := cp.loadConfig(DefaultConfigReplacements())
	if err != nil {
		return err
	}
	if ok := config.HasCloud(cloud); !ok {
		return fmt.Errorf("the cloud %s is not found in the config", cloud)
	}

	if ok := config.HasDeployEnv(cloud, deployEnv); !ok {
		return fmt.Errorf("the deployment env %s is not found under cloud %s", deployEnv, cloud)
	}
	return nil
}

func (cp *configProviderImpl) GetDeployEnvVariables(cloud, deployEnv string, configReplacements *ConfigReplacements) (Variables, error) {
	config, err := cp.loadConfig(configReplacements)
	if err != nil {
		return nil, err
	}
	err = cp.Validate(cloud, deployEnv)
	if err != nil {
		return nil, err
	}

	variables := Variables{}
	mergeVariables(variables, config.GetDefaults())
	mergeVariables(variables, config.GetCloudOverrides(cloud))
	mergeVariables(variables, config.GetDeployEnvOverrides(cloud, deployEnv))

	return variables, nil
}

func (cp *configProviderImpl) GetRegions(cloud, deployEnv string) ([]string, error) {
	config, err := cp.loadConfig(DefaultConfigReplacements())
	if err != nil {
		return nil, err
	}
	err = cp.Validate(cloud, deployEnv)
	if err != nil {
		return nil, err
	}
	regions := config.GetRegions(cloud, deployEnv)
	return regions, nil
}

func (cp *configProviderImpl) GetRegionOverrides(cloud, deployEnv, region string, configReplacements *ConfigReplacements) (Variables, error) {
	config, err := cp.loadConfig(configReplacements)
	if err != nil {
		return nil, err
	}
	return config.GetRegionOverrides(cloud, deployEnv, region), nil
}

func (cp *configProviderImpl) loadConfig(configReplacements *ConfigReplacements) (VariableOverrides, error) {
	// TODO validate that field names are unique regardless of casing
	// parse, execute and unmarshal the config file as a template to generate the final config file
	bytes, err := PreprocessFile(cp.config, configReplacements.AsMap())
	if err != nil {
		return nil, err
	}

	currentVariableOverrides := NewVariableOverrides()
	if err := yaml.Unmarshal(bytes, currentVariableOverrides); err == nil {
		return currentVariableOverrides, nil
	} else {
		return nil, err
	}
}

func PreprocessFile(templateFilePath string, vars map[string]interface{}) ([]byte, error) {
	tmpl := template.New("file")
	content, err := os.ReadFile(templateFilePath)
	if err != nil {
		return nil, err
	}

	tmpl, err = tmpl.Parse(string(content))
	if err != nil {
		return nil, err
	}

	var tmplBytes bytes.Buffer
	if err := tmpl.Option("missingkey=error").Execute(&tmplBytes, vars); err != nil {
		return nil, err
	}
	return tmplBytes.Bytes(), nil
}
