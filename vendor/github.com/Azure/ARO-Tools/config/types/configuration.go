package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/tools/yamlwrap"
)

// Configuration is the top-level container for all values for all services. See an example at: https://github.com/Azure/ARO-HCP/blob/main/config/config.yaml
type Configuration map[string]any

type MissingKeyError struct {
	Path string
	Key  string
}

func (e MissingKeyError) Error() string {
	return fmt.Sprintf("configuration%s: key %s not found", e.Path, e.Key)
}

// UnmarshalJSON overrides the default JSON unmarshaling to preserve integer
// precision. Without it, all numbers become float64 and values >= 1e6 render
// as scientific notation in Go templates (e.g. "2e+06").
func (c *Configuration) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	if raw == nil {
		*c = nil
		return nil
	}
	converted, ok := convertJSONNumbers(raw).(map[string]any)
	if !ok {
		return fmt.Errorf("failed to unmarshal configuration: expected map[string]any, got %T", converted)
	}
	*c = Configuration(converted)
	return nil
}

// convertJSONNumbers recursively walks a decoded JSON value and converts
// json.Number to int64 (whole numbers) or float64 (fractional).
func convertJSONNumbers(v any) any {
	switch val := v.(type) {
	case map[string]any:
		for k, inner := range val {
			val[k] = convertJSONNumbers(inner)
		}
		return val
	case []any:
		for i, inner := range val {
			val[i] = convertJSONNumbers(inner)
		}
		return val
	case json.Number:
		if i, err := val.Int64(); err == nil {
			return i
		}
		if f, err := val.Float64(); err == nil {
			return f
		}
		return val.String()
	default:
		return val
	}
}

func (v Configuration) GetByPath(path string) (any, error) {
	keys := strings.Split(path, ".")
	var current any = map[string]any(v)
	var currentPath string

	for _, key := range keys {
		if m, ok := current.(map[string]any); ok {
			current, ok = m[key]
			if !ok {
				return nil, &MissingKeyError{Path: currentPath, Key: key}
			}
		} else {
			return nil, fmt.Errorf("configuration%s: expected nested map, found %T; cannot index with %s", currentPath, current, key)
		}
		currentPath += "[" + key + "]"
	}

	return current, nil
}

// MergeConfiguration returns a new configuration holding keys from base, unless they have been overridden.
// This function does not mutate its inputs, but returns a `map[string]any` instead of `types.Configuration`, so
// if your consumer is sensitive to the distinction, remember to cast the output.
func MergeConfiguration(base, override Configuration) map[string]any {
	if base == nil {
		base = Configuration{}
	}
	if override == nil {
		override = Configuration{}
	}
	output := make(Configuration, len(base))
	for k, v := range base {
		output[k] = v
	}
	for k, newValue := range override {
		if baseValue, exists := output[k]; exists {
			srcMap, srcMapOk := newValue.(map[string]any)
			dstMap, dstMapOk := baseValue.(map[string]any)
			if srcMapOk && dstMapOk {
				newValue = MergeConfiguration(dstMap, srcMap)
			}
		}
		output[k] = newValue
	}

	return output
}

// resolveSchemaPath resolves a schema path for a new file location while preserving whether it's relative or absolute.
// - if the schema path is already absolute, it returns it as is
// - if the schema path is relative, it computes a new relative path from the target file to the schema
// - if the schema path is empty, it returns an empty string
func resolveSchemaPath(schemaPath, originalConfigDir, targetConfigDir string) (string, error) {
	if schemaPath == "" {
		return "", nil
	}

	if filepath.IsAbs(schemaPath) {
		return schemaPath, nil
	}

	absoluteSchemaPath, err := filepath.Abs(filepath.Join(originalConfigDir, schemaPath))
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path for schema path %q: %w", schemaPath, err)
	}

	absoluteTargetDir, err := filepath.Abs(targetConfigDir)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path for target directory %q: %w", targetConfigDir, err)
	}

	relativeSchemaPath, err := filepath.Rel(absoluteTargetDir, absoluteSchemaPath)
	if err != nil {
		return "", fmt.Errorf("failed to compute relative path from %q to %q: %w", targetConfigDir, absoluteSchemaPath, err)
	}

	return relativeSchemaPath, nil
}

// MergeRawConfigurationFiles merges multiple configuration files into a single configuration
// while rebasing the schema path to the proposed schemaLocationRebaseReference.
// The function is able to handle raw configuration files with Go template placeholders.
func MergeRawConfigurationFiles(schemaLocationRebaseReference string, configFilePaths []string) ([]byte, error) {
	if len(configFilePaths) == 0 {
		return nil, fmt.Errorf("no configuration files provided")
	}

	// iteratively merge the configuration files
	rawMerged := Configuration{}
	var targetFileSchemaPath string
	for _, configFile := range configFilePaths {
		rawConfig, err := readAndWrapRawConfig(configFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read configuration file %q: %w", configFile, err)
		}

		if rawConfigSchemaPath, hasSchema := rawConfig["$schema"]; hasSchema {
			if rawConfigSchemaPathStr, ok := rawConfigSchemaPath.(string); ok {
				targetFileSchemaPath, err = resolveSchemaPath(rawConfigSchemaPathStr, filepath.Dir(configFile), schemaLocationRebaseReference)
				if err != nil {
					return nil, fmt.Errorf("failed to resolve schema path %q: %w", rawConfigSchemaPathStr, err)
				}
			} else {
				return nil, fmt.Errorf("$schema in configuration file %q is not a string", configFile)
			}
		}
		rawMerged = MergeConfiguration(rawMerged, rawConfig)
	}
	if targetFileSchemaPath != "" {
		rawMerged["$schema"] = targetFileSchemaPath
	}

	// marshal and unwrap
	rawYaml, err := yaml.Marshal(rawMerged)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal configuration: %w", err)
	}
	unwrappedYaml, err := yamlwrap.UnwrapYAML(rawYaml)
	if err != nil {
		return nil, fmt.Errorf("failed to unwrap configuration: %w", err)
	}

	return unwrappedYaml, nil
}

// readAndWrapRawConfig reads a YAML file with Go template placeholders by wrapping it
// with yamlwrapper to make template syntax valid YAML, then parses it into a Configuration.
func readAndWrapRawConfig(filePath string) (Configuration, error) {
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read configuration file %q: %w", filePath, err)
	}

	wrappedRaw, err := yamlwrap.WrapYAML(raw, true)
	if err != nil {
		return nil, fmt.Errorf("failed to wrap configuration file %q: %w", filePath, err)
	}

	var config Configuration
	if err := yaml.Unmarshal(wrappedRaw, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal configuration file %q: %w", filePath, err)
	}

	return config, nil
}

// TruncateConfiguration returns a new configuration with specified paths excluded from the base configuration.
// Paths use dot notation (e.g., "database.host", "api.endpoints.users").
// Returns an error if config is nil, no paths are provided, or if any path is invalid.
func TruncateConfiguration(config Configuration, paths ...string) (map[string]any, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no paths provided for truncation")
	}

	// Validate paths
	for _, path := range paths {
		if err := validateTruncatePath(path); err != nil {
			return nil, fmt.Errorf("invalid truncate path %q: %w", path, err)
		}
	}

	result := truncateConfigurationRecursive(map[string]any(config), sets.New(paths...), "")
	return result, nil
}

// validPathRegex matches valid dot notation paths (one or more segments separated by dots)
var validPathRegex = regexp.MustCompile(`^[^.]+(\.[^.]+)*$`)

// validateTruncatePath validates that a path is in proper dot notation format
func validateTruncatePath(path string) error {
	if !validPathRegex.MatchString(path) {
		return fmt.Errorf("path must be in dot notation format (e.g., 'key' or 'parent.child')")
	}
	return nil
}

// truncateConfigurationRecursive recursively copies the configuration while excluding specified paths
func truncateConfigurationRecursive(current map[string]any, truncatePaths sets.Set[string], currentPath string) map[string]any {
	if current == nil {
		return nil
	}

	output := make(map[string]any)

	for key, value := range current {
		var fullPath string
		if currentPath == "" {
			fullPath = key
		} else {
			fullPath = currentPath + "." + key
		}

		if truncatePaths.Has(fullPath) {
			continue
		}

		if nestedMap, ok := value.(map[string]any); ok {
			result := truncateConfigurationRecursive(nestedMap, truncatePaths, fullPath)
			output[key] = result
		} else {
			// Not a nested map, copy the value as-is
			output[key] = value
		}
	}

	return output
}
