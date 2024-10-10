package config

import (
	"bytes"
	"context"
	"text/template"

	"gopkg.in/yaml.v3"
)

func NewConfigProvider(config, region, user string) *configProviderImpl {
	return &configProviderImpl{
		config: config,
		region: region,
		user:   user,
	}
}

// get the variables toke effect finally for cloud/deployEnv/region
func (cp *configProviderImpl) GetVariables(ctx context.Context, cloud, deployEnv string) (Variables, error) {
	variableOverrides, err := cp.loadConfig()
	variables := Variables{}

	if err == nil {
		for k, v := range variableOverrides.Defaults {
			variables[k] = v
		}
		if cloudOverride, ok := variableOverrides.Overrides[cloud]; ok {
			for k, v := range cloudOverride.Defaults {
				variables[k] = v
			}

			if deployEnvOverride, ok := cloudOverride.Overrides[deployEnv]; ok {
				for k, v := range deployEnvOverride.Defaults {
					variables[k] = v
				}

				if regionOverride, ok := deployEnvOverride.Overrides[cp.region]; ok {
					for k, v := range regionOverride {
						variables[k] = v
					}
				}
			}
		}
	}

	return variables, err
}

func (cp *configProviderImpl) loadConfig() (*VariableOverrides, error) {
	tmpl, err := template.ParseFiles(cp.config)

	if err == nil {
		var tmplBytes bytes.Buffer

		if err := tmpl.Execute(&tmplBytes, Variables{"region": cp.region, "user": cp.user}); err == nil {
			currentVariableOverrides := &VariableOverrides{}

			if err := yaml.Unmarshal(tmplBytes.Bytes(), currentVariableOverrides); err == nil {
				cp.baseVariableOverrides = currentVariableOverrides
			}
		}
	}

	return cp.baseVariableOverrides, err
}
