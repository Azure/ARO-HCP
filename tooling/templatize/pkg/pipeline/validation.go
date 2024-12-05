package pipeline

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

//go:embed pipeline.schema.v1.json
var pipelineSchemaV1Content []byte

func ValidatePipelineSchema(pipelineContent []byte) error {
	// unmarshal pipeline content
	pipelineMap := make(map[string]interface{})
	err := yaml.Unmarshal(pipelineContent, &pipelineMap)
	if err != nil {
		return fmt.Errorf("failed to unmarshal pipeline YAML content: %v", err)
	}

	// load pipeline schema
	pipelineSchema, schemaUrl, err := getSchemaForPipeline(pipelineMap)
	if err != nil {
		return fmt.Errorf("failed to load pipeline schema: %v", err)
	}

	// validate pipeline schema
	err = pipelineSchema.Validate(pipelineMap)
	if err != nil {
		return fmt.Errorf("pipeline is not compliant with schema %s: %v", schemaUrl, err)
	}
	return nil
}

func getSchemaForPipeline(pipelineMap map[string]interface{}) (*jsonschema.Schema, string, error) {
	schemaRef, ok := pipelineMap["$schema"].(string)
	if !ok {
		return nil, "", fmt.Errorf("pipeline $schema reference is missing - add $schema: pipeline.schema.v1")
	}

	switch schemaRef {
	case "pipeline.schema.v1":
		return compileSchema(pipelineSchemaV1Content)
	default:
		return nil, "", fmt.Errorf("unsupported schema reference: %s", schemaRef)
	}
}

func compileSchema(schemaBytes []byte) (*jsonschema.Schema, string, error) {
	// parse schema content
	schemaMap := make(map[string]interface{})
	err := json.Unmarshal(schemaBytes, &schemaMap)
	if err != nil {
		return nil, "", fmt.Errorf("failed to unmarshal schema content: %v", err)
	}
	schemaUrl, ok := schemaMap["title"].(string)
	if !ok {
		return nil, "", fmt.Errorf("failed to get schema title")
	}

	// compile schema
	c := jsonschema.NewCompiler()
	err = c.AddResource(schemaUrl, schemaMap)
	if err != nil {
		return nil, "", fmt.Errorf("failed to add schema resource %s: %v", schemaUrl, err)
	}
	pipelineSchema, err := c.Compile(schemaUrl)
	if err != nil {
		return nil, "", fmt.Errorf("failed to compile schema %s: %v", schemaUrl, err)
	}

	return pipelineSchema, schemaUrl, nil
}

func (p *Pipeline) Validate() error {
	// collect all steps from all resourcegroups and fail if there are duplicates
	stepMap := make(map[string]*Step)
	for _, rg := range p.ResourceGroups {
		for _, step := range rg.Steps {
			if _, ok := stepMap[step.Name]; ok {
				return fmt.Errorf("duplicate step name %q", step.Name)
			}
			stepMap[step.Name] = step
		}
	}

	// validate dependsOn for a step exists
	for _, step := range stepMap {
		for _, dep := range step.DependsOn {
			if _, ok := stepMap[dep]; !ok {
				return fmt.Errorf("invalid dependency on step %s: dependency %s does not exist", step.Name, dep)
			}
		}
	}

	// todo check for circular dependencies

	// validate resource groups
	for _, rg := range p.ResourceGroups {
		err := rg.Validate()
		if err != nil {
			return err
		}
	}
	return nil
}

func (rg *ResourceGroup) Validate() error {
	if rg.Name == "" {
		return fmt.Errorf("resource group name is required")
	}
	if rg.Subscription == "" {
		return fmt.Errorf("subscription is required")
	}
	return nil
}
