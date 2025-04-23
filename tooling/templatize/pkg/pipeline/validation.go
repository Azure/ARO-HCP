// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package pipeline

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

//go:embed pipeline.schema.v1.json
var pipelineSchemaV1Content []byte
var pipelineSchemaV1Ref = "pipeline.schema.v1"
var defaultSchemaRef = pipelineSchemaV1Ref

func ValidatePipelineSchema(pipelineContent []byte) error {
	// unmarshal pipeline content
	pipelineMap := make(map[string]interface{})
	err := yaml.Unmarshal(pipelineContent, &pipelineMap)
	if err != nil {
		return fmt.Errorf("failed to unmarshal pipeline YAML content: %v", err)
	}

	// load pipeline schema
	pipelineSchema, schemaRef, err := getSchemaForPipeline(pipelineMap)
	if err != nil {
		return fmt.Errorf("failed to load pipeline schema: %v", err)
	}

	// validate pipeline schema
	err = pipelineSchema.Validate(pipelineMap)
	if err != nil {
		return fmt.Errorf("pipeline is not compliant with schema %s: %v", schemaRef, err)
	}
	return nil
}

func ValidatePipelineSchemaForStruct(pipeline *Pipeline) error {
	pipelineBytes, err := yaml.Marshal(pipeline)
	if err != nil {
		return fmt.Errorf("failed to marshal pipeline: %w", err)
	}

	return ValidatePipelineSchema(pipelineBytes)
}

func getSchemaForPipeline(pipelineMap map[string]interface{}) (pipelineSchema *jsonschema.Schema, schemaRef string, err error) {
	schemaRef, _ = pipelineMap["$schema"].(string)
	return getSchemaForRef(schemaRef)
}

func getSchemaForRef(schemaRef string) (*jsonschema.Schema, string, error) {
	if schemaRef == "" {
		schemaRef = defaultSchemaRef
	}
	switch schemaRef {
	case pipelineSchemaV1Ref:
		pipelineSchema, err := compileSchema(schemaRef, pipelineSchemaV1Content)
		return pipelineSchema, schemaRef, err
	default:
		return nil, "", fmt.Errorf("unsupported schema reference: %s", schemaRef)
	}
}

func getVariableRefStepProperties(pipelineSchema *jsonschema.Schema) (map[string]*jsonschema.Schema, error) {
	stepProperties := pipelineSchema.Properties["resourceGroups"].Items.(*jsonschema.Schema).Properties["steps"].Items.(*jsonschema.Schema).Properties
	variableRefProperties := make(map[string]*jsonschema.Schema)
	for propName, propValue := range stepProperties {
		if propValue.Ref != nil && strings.HasSuffix(propValue.Ref.Location, "#/definitions/variableRef") {
			variableRefProperties[propName] = propValue
		}
	}
	return variableRefProperties, nil
}

func compileSchema(schemaRef string, schemaBytes []byte) (*jsonschema.Schema, error) {
	// parse schema content
	schemaMap := make(map[string]interface{})
	err := json.Unmarshal(schemaBytes, &schemaMap)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal schema content: %v", err)
	}

	// compile schema
	c := jsonschema.NewCompiler()
	err = c.AddResource(schemaRef, schemaMap)
	if err != nil {
		return nil, fmt.Errorf("failed to add schema resource %s: %v", schemaRef, err)
	}
	pipelineSchema, err := c.Compile(schemaRef)
	if err != nil {
		return nil, fmt.Errorf("failed to compile schema %s: %v", schemaRef, err)
	}

	return pipelineSchema, nil
}

func (p *Pipeline) Validate() error {
	// collect all steps from all resourcegroups and fail if there are duplicates
	stepMap := make(map[string]Step)
	for _, rg := range p.ResourceGroups {
		for _, step := range rg.Steps {
			if _, ok := stepMap[step.StepName()]; ok {
				return fmt.Errorf("duplicate step name %q", step.StepName())
			}
			stepMap[step.StepName()] = step
		}
	}

	// validate dependsOn for a step exists
	for _, step := range stepMap {
		for _, dep := range step.Dependencies() {
			if _, ok := stepMap[dep]; !ok {
				return fmt.Errorf("invalid dependency on step %s: dependency %s does not exist", step.StepName(), dep)
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
