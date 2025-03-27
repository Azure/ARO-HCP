package config

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"text/template"

	"github.com/santhosh-tekuri/jsonschema/v6"
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
func MergeVariables(base, override Variables) Variables {
	for k, newValue := range override {
		if baseValue, exists := base[k]; exists {
			srcMap, srcMapOk := InterfaceToVariables(newValue)
			dstMap, dstMapOk := InterfaceToVariables(baseValue)
			if srcMapOk && dstMapOk {
				newValue = MergeVariables(dstMap, srcMap)
			}
		}
		base[k] = newValue
	}

	return base
}

// Needed to convert Variables to map[string]interface{} for jsonschema validation
// see: https://github.com/santhosh-tekuri/jsonschema/blob/boon/schema.go#L124
func convertToInterface(variables Variables) map[string]any {
	m := map[string]any{}
	for k, v := range variables {
		if subMap, ok := v.(Variables); ok {
			m[k] = convertToInterface(subMap)
		} else {
			m[k] = v
		}
	}
	return m
}

func isUrl(str string) bool {
	u, err := url.Parse(str)
	return err == nil && u.Scheme != "" && u.Host != ""
}

func (cp *configProviderImpl) loadSchema() (any, error) {
	schemaPath := cp.schema

	var reader io.Reader
	var err error

	if isUrl(schemaPath) {
		resp, err := http.Get(schemaPath)
		if err != nil {
			return nil, fmt.Errorf("failed to get schema file: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("faild to get schema file, statuscode %v", resp.StatusCode)
		}
		reader = resp.Body
	} else {
		if !filepath.IsAbs(schemaPath) {
			schemaPath = filepath.Join(filepath.Dir(cp.config), schemaPath)
		}
		reader, err = os.Open(schemaPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open schema file: %v", err)
		}
	}

	schema, err := jsonschema.UnmarshalJSON(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal schema: %v", err)
	}

	return schema, nil
}

func (cp *configProviderImpl) validateSchema(variables Variables) error {
	c := jsonschema.NewCompiler()

	schema, err := cp.loadSchema()
	if err != nil {
		return fmt.Errorf("failed to load schema: %v", err)
	}

	err = c.AddResource(cp.schema, schema)
	if err != nil {
		return fmt.Errorf("failed to add schema resource: %v", err)
	}
	sch, err := c.Compile(cp.schema)
	if err != nil {
		return fmt.Errorf("failed to compile schema: %v", err)
	}

	err = sch.Validate(convertToInterface(variables))
	if err != nil {
		return fmt.Errorf("failed to validate schema: %v", err)
	}
	return nil
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
	MergeVariables(variables, regionOverrides)

	// validate schema
	err = cp.validateSchema(variables)
	if err != nil {
		return nil, err
	}
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

	if !config.HasSchema() {
		return fmt.Errorf("$schema not found in config")
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
	MergeVariables(variables, config.GetDefaults())
	MergeVariables(variables, config.GetCloudOverrides(cloud))
	MergeVariables(variables, config.GetDeployEnvOverrides(cloud, deployEnv))

	cp.schema = config.GetSchema()

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
	rawContent, err := PreprocessFile(cp.config, configReplacements.AsMap())
	if err != nil {
		return nil, err
	}

	currentVariableOverrides := NewVariableOverrides()
	if err := yaml.Unmarshal(rawContent, currentVariableOverrides); err == nil {
		return currentVariableOverrides, nil
	} else {
		return nil, err
	}
}

// PreprocessFile reads and processes a gotemplate
// The path will be read as is. It parses the file as a template, and executes it with the provided variables.
func PreprocessFile(templateFilePath string, vars map[string]any) ([]byte, error) {
	content, err := os.ReadFile(templateFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", templateFilePath, err)
	}
	processedContent, err := PreprocessContent(content, vars)
	if err != nil {
		return nil, fmt.Errorf("failed to preprocess file %s: %w", templateFilePath, err)
	}
	return processedContent, nil
}

// PreprocessContent processes a gotemplate from memory
func PreprocessContent(content []byte, vars map[string]any) ([]byte, error) {
	var tmplBytes bytes.Buffer
	if err := PreprocessContentIntoWriter(content, vars, &tmplBytes); err != nil {
		return nil, err
	}
	return tmplBytes.Bytes(), nil
}

func PreprocessContentIntoWriter(content []byte, vars map[string]any, writer io.Writer) error {
	tmpl, err := template.New("file").Parse(string(content))
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	if err := tmpl.Option("missingkey=error").Execute(writer, vars); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}
	return nil
}
