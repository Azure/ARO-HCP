package config

import (
	"bytes"
	"fmt"
	"os"
	"text/template"

	"gopkg.in/yaml.v3"

	"github.com/Azure/ARO-HCP/tooling/templatize/internal/naming"
)

type Provider interface {
	GetVariables(cloud, deployEnv string) (Variables, error)
}

func NewConfigProvider(config, region, regionStamp, cxStamp string) *configProviderImpl {
	return &configProviderImpl{
		config:      config,
		region:      region,
		regionStamp: regionStamp,
		cxStamp:     cxStamp,
	}
}

// get the variables toke effect finally for cloud/deployEnv/region
func (cp *configProviderImpl) GetVariables(cloud, deployEnv string, extraVars map[string]string) (Variables, error) {
	variableOverrides, err := cp.loadConfig(cloud, deployEnv)
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
			} else {
				return nil, fmt.Errorf("the deployment env %s is not found under cloud %s in %s", deployEnv, cloud, cp.config)
			}
		}
	}

	if _, exists := variables["extraVars"]; exists {
		return nil, fmt.Errorf("extraVars is a reserved key and cannot be used in the config file")
	}

	if len(extraVars) > 0 {
		variables["extraVars"] = extraVars
	}
	return variables, err
}

func (cp *configProviderImpl) loadConfig(cloud, deployEnv string) (*VariableOverrides, error) {
	vars := map[string]interface{}{
		"ctx": map[string]interface{}{
			"region":      cp.region,
			"cloud":       cloud,
			"deployEnv":   deployEnv,
			"regionStamp": cp.regionStamp,
			"cxStamp":     cp.cxStamp,
		},
	}

	functions := template.FuncMap{
		"azureEventGridName":      naming.AzureEventGridName,
		"azurePostgresName":       naming.AzurePostgresName,
		"azureKeyVaultName":       naming.AzureKeyVaultName,
		"azureStorageAccountName": naming.AzureStorageAccountName,
		"azureCosmosDBName":       naming.AzureCosmosDBName,
		"uniqueString":            naming.UniqueString,
	}

	// parse, execute and unmarshal the config file as a template to generate the final config file
	tmpl := template.New("configTemplate").Funcs(functions)
	content, err := os.ReadFile(cp.config)
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

	currentVariableOverrides := &VariableOverrides{}
	if err := yaml.Unmarshal(tmplBytes.Bytes(), currentVariableOverrides); err == nil {
		cp.baseVariableOverrides = currentVariableOverrides
	} else {
		return nil, err
	}

	return cp.baseVariableOverrides, err
}
