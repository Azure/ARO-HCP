package pipeline

import (
	"context"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type subsciptionLookup func(context.Context, string) (string, error)

type Pipeline struct {
	schema           string `yaml:"$schema,omitempty"`
	pipelineFilePath string
	ServiceGroup     string           `yaml:"serviceGroup"`
	RolloutName      string           `yaml:"rolloutName"`
	ResourceGroups   []*ResourceGroup `yaml:"resourceGroups"`
}

type ResourceGroup struct {
	Name         string `yaml:"name"`
	Subscription string `yaml:"subscription"`
	AKSCluster   string `yaml:"aksCluster,omitempty"`
	Steps        []Step `yaml:"steps"`
}

func NewPlainPipelineFromBytes(filepath string, bytes []byte) (*Pipeline, error) {
	rawPipeline := &struct {
		Schema         string `yaml:"$schema,omitempty"`
		ServiceGroup   string `yaml:"serviceGroup"`
		RolloutName    string `yaml:"rolloutName"`
		ResourceGroups []struct {
			Name         string           `yaml:"name"`
			Subscription string           `yaml:"subscription"`
			AKSCluster   string           `yaml:"aksCluster,omitempty"`
			Steps        []map[string]any `yaml:"steps"`
		} `yaml:"resourceGroups"`
	}{}
	err := yaml.Unmarshal(bytes, rawPipeline)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal pipeline: %w", err)
	}

	// find step properties that are variableRefs
	pipelineSchema, _, err := getSchemaForRef(rawPipeline.Schema)
	if err != nil {
		return nil, fmt.Errorf("failed to get schema for pipeline: %w", err)
	}
	variableRefStepProperties, err := getVariableRefStepProperties(pipelineSchema)
	if err != nil {
		return nil, fmt.Errorf("failed to get variableRef step properties: %w", err)
	}

	pipeline := &Pipeline{
		schema:           rawPipeline.Schema,
		pipelineFilePath: filepath,
		ServiceGroup:     rawPipeline.ServiceGroup,
		RolloutName:      rawPipeline.RolloutName,
		ResourceGroups:   make([]*ResourceGroup, len(rawPipeline.ResourceGroups)),
	}

	for i, rawRg := range rawPipeline.ResourceGroups {
		rg := &ResourceGroup{}
		pipeline.ResourceGroups[i] = rg
		rg.Name = rawRg.Name
		rg.Subscription = rawRg.Subscription
		rg.AKSCluster = rawRg.AKSCluster
		rg.Steps = make([]Step, len(rawRg.Steps))
		for i, rawStep := range rawRg.Steps {
			// preprocess variableRef step properties
			for propName := range rawStep {
				if _, ok := variableRefStepProperties[propName]; ok {
					variableRef := rawStep[propName].(map[string]any)
					variableRef["name"] = propName
				}
			}

			// unmarshal the map into a StepMeta
			stepMeta := &StepMeta{}
			err := mapToStruct(rawStep, stepMeta)
			if err != nil {
				return nil, err
			}
			switch stepMeta.Action {
			case "Shell":
				rg.Steps[i] = &ShellStep{}
			case "ARM":
				rg.Steps[i] = &ARMStep{}
			default:
				rg.Steps[i] = &GenericStep{}
			}
			err = mapToStruct(rawStep, rg.Steps[i])
			if err != nil {
				return nil, err
			}
		}
	}

	// another round of validation after postprocessing
	err = ValidatePipelineSchemaForStruct(pipeline)
	if err != nil {
		return nil, fmt.Errorf("pipeline schema validation failed after postprocessing: %w", err)
	}

	return pipeline, nil
}

func mapToStruct(m any, s interface{}) error {
	bytes, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(bytes, s); err != nil {
		return err

	}
	return nil
}

type outPutHandler func(string)

type StepMeta struct {
	Name      string   `yaml:"name"`
	Action    string   `yaml:"action"`
	DependsOn []string `yaml:"dependsOn,omitempty"`
}

func (m *StepMeta) StepName() string {
	return m.Name
}

func (m *StepMeta) ActionType() string {
	return m.Action
}

func (m *StepMeta) Dependencies() []string {
	return m.DependsOn
}

type Step interface {
	StepName() string
	ActionType() string
	Description() string
	Dependencies() []string
}

func NewShellStep(name string, command string) *ShellStep {
	return &ShellStep{
		StepMeta: StepMeta{
			Name:   name,
			Action: "Shell",
		},
		Command: command,
	}
}

type ShellStep struct {
	StepMeta   `yaml:",inline"`
	Command    string     `yaml:"command,omitempty"`
	Variables  []Variable `yaml:"variables,omitempty"`
	DryRun     DryRun     `yaml:"dryRun,omitempty"`
	outputFunc outPutHandler
}

func (s *ShellStep) Description() string {
	return fmt.Sprintf("Step %s\n  Kind: %s\n  Command: %s\n", s.Name, s.Action, s.Command)
}

func (s *ShellStep) WithDependsOn(dependsOn ...string) *ShellStep {
	s.DependsOn = dependsOn
	return s
}

func (s *ShellStep) WithVariables(variables ...Variable) *ShellStep {
	s.Variables = variables
	return s
}

func (s *ShellStep) WithDryRun(dryRun DryRun) *ShellStep {
	s.DryRun = dryRun
	return s
}

func (s *ShellStep) WithOutputFunc(outputFunc outPutHandler) *ShellStep {
	s.outputFunc = outputFunc
	return s
}

func NewARMStep(name string, template string, parameters string, deploymentLevel string) *ARMStep {
	return &ARMStep{
		StepMeta: StepMeta{
			Name:   name,
			Action: "ARM",
		},
		Template:        template,
		Parameters:      parameters,
		DeploymentLevel: deploymentLevel,
	}
}

func (s *ARMStep) WithDependsOn(dependsOn ...string) *ARMStep {
	s.DependsOn = dependsOn
	return s
}

func (s *ARMStep) WithVariables(variables ...Variable) *ARMStep {
	s.Variables = variables
	return s
}

func (s *ARMStep) WithOutputOnly() *ARMStep {
	s.OutputOnly = true
	return s
}

type ARMStep struct {
	StepMeta        `yaml:",inline"`
	Command         string     `yaml:"command,omitempty"`
	Variables       []Variable `yaml:"variables,omitempty"`
	Template        string     `yaml:"template,omitempty"`
	Parameters      string     `yaml:"parameters,omitempty"`
	DeploymentLevel string     `yaml:"deploymentLevel,omitempty"`
	OutputOnly      bool       `yaml:"outputOnly,omitempty"`
}

func (s *ARMStep) Description() string {
	var details []string
	details = append(details, fmt.Sprintf("Template: %s", s.Template))
	details = append(details, fmt.Sprintf("Parameters: %s", s.Parameters))
	return fmt.Sprintf("Step %s\n  Kind: %s\n  %s", s.Name, s.Action, strings.Join(details, "\n  "))
}

type GenericStep struct {
	StepMeta `yaml:",inline"`
	Body     map[string]any `yaml:",inline"`
}

func (s *GenericStep) Description() string {
	return fmt.Sprintf("Step %s\n  Kind: %s", s.Name, s.Action)
}

type DryRun struct {
	Variables []Variable `yaml:"variables,omitempty"`
	Command   string     `yaml:"command,omitempty"`
}

type Variable struct {
	Name      string `yaml:"name,omitempty"`
	ConfigRef string `yaml:"configRef,omitempty"`
	Value     string `yaml:"value,omitempty"`
	Input     *Input `yaml:"input,omitempty"`
}

type Input struct {
	Name string `yaml:"name"`
	Step string `yaml:"step"`
}
