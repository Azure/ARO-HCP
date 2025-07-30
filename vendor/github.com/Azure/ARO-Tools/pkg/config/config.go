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

package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/template"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/pkg/config/ev2config"
	"github.com/Azure/ARO-Tools/pkg/config/types"
)

// ConfigReplacements holds replacement values
type ConfigReplacements struct {
	RegionReplacement      string
	RegionShortReplacement string
	StampReplacement       string
	CloudReplacement       string
	EnvironmentReplacement string

	Ev2Config map[string]interface{}
}

// AsMap returns a map[string]interface{} representation of this ConfigReplacement instance
func (c ConfigReplacements) AsMap() map[string]interface{} {
	m := map[string]interface{}{
		"ctx": map[string]interface{}{
			"region":      c.RegionReplacement,
			"regionShort": c.RegionShortReplacement,
			"stamp":       c.StampReplacement,
			"cloud":       c.CloudReplacement,
			"environment": c.EnvironmentReplacement,
		},
		"ev2": c.Ev2Config,
	}
	return m
}

// ConfigProvider provides service configuration using a base configuration file.
type ConfigProvider interface {
	// AllContexts determines all the clouds, environments, and regions that this provider has explicit records for.
	AllContexts() map[string]map[string][]string
	// GetResolver consumes the configuration replacements to create a configuration resolver.
	// The cloud and environment provided in the replacements must be literal values, used to
	// constrain the resolver further and ensure that configurations it resolves are correct.
	GetResolver(configReplacements *ConfigReplacements) (ConfigResolver, error)
}

// ConfigResolver resolves service configuration for a specific environment and cloud using a processed configuration file.
type ConfigResolver interface {
	// ValidateSchema validates a fully resolved configuration created by this provider.
	ValidateSchema(config types.Configuration) error
	// SchemaPath returns the absolute path to the JSONSchema file that this config is registered as using.
	SchemaPath() (string, error)
	// GetConfiguration resolves the configuration for the cloud and environment.
	GetConfiguration() (types.Configuration, error)
	// GetRegions divulges the regions for which overrides are registered.
	GetRegions() ([]string, error)
	// GetRegionConfiguration resolves the configuration for a region in the cloud and environment.
	GetRegionConfiguration(region string) (types.Configuration, error)
	// GetRegionOverrides fetches the overrides specific to a region, if any exist.
	GetRegionOverrides(region string) (types.Configuration, error)
}

// NewConfigProvider creates a configuration provider by knowing the path to the configuration file.
// Configuration files are not valid YAML - they are text templates that, when provided with the correct set of inputs
// and run through the Go template engine, become valid YAML. We want to be able to load the whole config file before
// the user knows what cloud/env/region they want, since we have a lot of cases where we want to operate over all the
// cloud/env/regions in the config. Previously, we had said that the whole config file needs to parse as valid YAML
// before templating, but this unfortunately means you cannot use templates for non-string values, which is a non-starter.
//
// We instead run the file through a dummy template step that replaces values with some cloud/env/region and - since
// this ConfigProvider can't do anything except for divulge the contexts - we can just do the correct templating later.
func NewConfigProvider(config string) (ConfigProvider, error) {
	cp := configProvider{
		path: config,
	}

	raw, err := os.ReadFile(config)
	if err != nil {
		return nil, err
	}
	cp.raw = raw

	ev2Cfg, err := ev2config.ResolveConfig("public", "uksouth")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve ev2 configuration: %w", err)
	}

	rawContent, err := PreprocessContent(cp.raw, ConfigReplacements{
		CloudReplacement:       "public",
		EnvironmentReplacement: "int",
		RegionReplacement:      "uksouth",
		RegionShortReplacement: "ln",
		StampReplacement:       "1",
		Ev2Config:              ev2Cfg,
	}.AsMap())
	if err != nil {
		return nil, err
	}

	if err := yaml.Unmarshal(rawContent, &cp.withFakeReplacements); err != nil {
		return nil, err
	}

	return &cp, nil
}

type configProvider struct {
	// schema can be a relative path to this file, so we need to keep track of it
	path                 string
	raw                  []byte
	withFakeReplacements configurationOverrides
}

// AllContexts returns all clouds, environments and regions in the configuration.
func (cp *configProvider) AllContexts() map[string]map[string][]string {
	contexts := map[string]map[string][]string{}
	for cloud, cloudCfg := range cp.withFakeReplacements.Overrides {
		contexts[cloud] = map[string][]string{}
		for environment, envCfg := range cloudCfg.Overrides {
			contexts[cloud][environment] = []string{}
			for region := range envCfg.Overrides {
				contexts[cloud][environment] = append(contexts[cloud][environment], region)
			}
		}
	}
	return contexts
}

func (cp *configProvider) GetResolver(configReplacements *ConfigReplacements) (ConfigResolver, error) {
	for description, value := range map[string]*string{
		"cloud":       &configReplacements.CloudReplacement,
		"environment": &configReplacements.EnvironmentReplacement,
	} {
		if value == nil || *value == "" {
			return nil, fmt.Errorf("%q override is required", description)
		}
	}

	// TODO validate that field names are unique regardless of casing
	// parse, execute and unmarshal the config file as a template to generate the final config file
	rawContent, err := PreprocessContent(cp.raw, configReplacements.AsMap())
	if err != nil {
		return nil, err
	}

	currentVariableOverrides := configurationOverrides{}
	if err := yaml.Unmarshal(rawContent, &currentVariableOverrides); err != nil {
		return nil, err
	}
	return &configResolver{
		cloud:       configReplacements.CloudReplacement,
		environment: configReplacements.EnvironmentReplacement,
		cfg:         currentVariableOverrides,
		path:        cp.path,
	}, nil
}

type configResolver struct {
	cloud, environment string
	cfg                configurationOverrides
	path               string
}

// Merges Configuration, returns merged Configuration
// DEPRECATED: use the exported function from types package instead
func MergeConfiguration(base, override Configuration) Configuration {
	return types.MergeConfiguration(base, override)
}

func (cr *configResolver) ValidateSchema(config types.Configuration) error {
	loader := jsonschema.SchemeURLLoader{
		"file": jsonschema.FileLoader{},
	}
	c := jsonschema.NewCompiler()
	c.UseLoader(loader)
	path, err := cr.SchemaPath()
	if err != nil {
		return err
	}
	sch, err := c.Compile(path)
	if err != nil {
		return fmt.Errorf("failed to compile schema: %v", err)
	}

	err = sch.Validate(map[string]any(config))
	if err != nil {
		return fmt.Errorf("failed to validate schema: %v", err)
	}
	return nil
}

func (cr *configResolver) SchemaPath() (string, error) {
	path := cr.cfg.Schema
	if !filepath.IsAbs(path) {
		absPath, err := filepath.Abs(filepath.Join(filepath.Dir(cr.path), path))
		if err != nil {
			return "", fmt.Errorf("failed to create absolute path to schema %q: %w", path, err)
		}
		path = absPath
	}
	return path, nil
}

func (cr *configResolver) GetRegions() ([]string, error) {
	cloudCfg, hasCloud := cr.cfg.Overrides[cr.cloud]
	if !hasCloud {
		return nil, fmt.Errorf("the cloud %s is not found in the config", cr.cloud)
	}
	envCfg, hasEnv := cloudCfg.Overrides[cr.environment]
	if !hasEnv {
		return nil, fmt.Errorf("the deployment env %s is not found under cloud %s", cr.environment, cr.cloud)
	}

	var regions []string
	for region := range envCfg.Overrides {
		regions = append(regions, region)
	}
	return regions, nil
}

// GetRegionConfiguration merges values to resolve the configuration for a region.
func (cr *configResolver) GetRegionConfiguration(region string) (types.Configuration, error) {
	cfg := cr.cfg.Defaults
	cloudCfg, hasCloud := cr.cfg.Overrides[cr.cloud]
	if !hasCloud {
		return nil, fmt.Errorf("the cloud %s is not found in the config", cr.cloud)
	}
	types.MergeConfiguration(cfg, cloudCfg.Defaults)
	envCfg, hasEnv := cloudCfg.Overrides[cr.environment]
	if !hasEnv {
		return nil, fmt.Errorf("the deployment env %s is not found under cloud %s", cr.environment, cr.cloud)
	}
	types.MergeConfiguration(cfg, envCfg.Defaults)
	regionCfg, hasRegion := envCfg.Overrides[region]
	if !hasRegion {
		// a missing region just means we use default values
		regionCfg = types.Configuration{}
	}
	types.MergeConfiguration(cfg, regionCfg)
	return cfg, nil
}

// GetConfiguration merges values to resolve the configuration for this cloud and environment.
func (cr *configResolver) GetConfiguration() (types.Configuration, error) {
	cfg := cr.cfg.Defaults
	cloudCfg, hasCloud := cr.cfg.Overrides[cr.cloud]
	if !hasCloud {
		return nil, fmt.Errorf("the cloud %s is not found in the config", cr.cloud)
	}
	types.MergeConfiguration(cfg, cloudCfg.Defaults)
	envCfg, hasEnv := cloudCfg.Overrides[cr.environment]
	if !hasEnv {
		return nil, fmt.Errorf("the deployment env %s is not found under cloud %s", cr.environment, cr.cloud)
	}
	types.MergeConfiguration(cfg, envCfg.Defaults)

	return cfg, nil
}

// GetRegionOverrides resolves the overrides for a region.
func (cr *configResolver) GetRegionOverrides(region string) (types.Configuration, error) {
	cloudCfg, hasCloud := cr.cfg.Overrides[cr.cloud]
	if !hasCloud {
		return nil, fmt.Errorf("the cloud %s is not found in the config", cr.cloud)
	}
	envCfg, hasEnv := cloudCfg.Overrides[cr.environment]
	if !hasEnv {
		return nil, fmt.Errorf("the deployment env %s is not found under cloud %s", cr.environment, cr.cloud)
	}
	regionCfg, hasRegion := envCfg.Overrides[region]
	if !hasRegion {
		// a missing region just means we use default values
		regionCfg = types.Configuration{}
	}
	return regionCfg, nil
}

// PreprocessFile reads and processes a gotemplate
// The path will be read as is. It parses the file as a template, and executes it with the provided Configuration.
func PreprocessFile(templateFilePath string, vars map[string]any) ([]byte, error) {
	content, err := os.ReadFile(templateFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", templateFilePath, err)
	}
	processedContent, err := PreprocessContent(content, vars)
	if err != nil {
		return nil, fmt.Errorf("failed to preprocess content %s: %w", templateFilePath, err)
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
