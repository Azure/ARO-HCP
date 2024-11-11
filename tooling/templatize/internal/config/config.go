package config

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
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

func interfaceToVariables(i interface{}) (Variables, bool) {
	// Helper, that reduces need for reflection calls, i.e. MapIndex
	// from: https://github.com/peterbourgon/mergemap/blob/master/mergemap.go
	value := reflect.ValueOf(i)
	if value.Kind() == reflect.Map {
		m := Variables{}
		for _, k := range value.MapKeys() {
			m[k.String()] = value.MapIndex(k).Interface()
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
			srcMap, srcMapOk := interfaceToVariables(newValue)
			dstMap, dstMapOk := interfaceToVariables(baseValue)
			if srcMapOk && dstMapOk {
				newValue = mergeVariables(dstMap, srcMap)
			}
		}
		base[k] = newValue
	}

	return base
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
			mergeVariables(variables, cloudOverride.Defaults)
			if deployEnvOverride, ok := cloudOverride.Overrides[deployEnv]; ok {
				mergeVariables(variables, deployEnvOverride.Defaults)
				if regionOverride, ok := deployEnvOverride.Overrides[cp.region]; ok {
					mergeVariables(variables, regionOverride)
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
