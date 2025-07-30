package types

import (
	"fmt"
	"strings"
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

// Merges Configuration, returns merged Configuration
// However the return value is only used for recursive updates on the map
// The actual merged Configuration are updated in the base map
func MergeConfiguration(base, override Configuration) map[string]any {
	if base == nil {
		base = Configuration{}
	}
	if override == nil {
		override = Configuration{}
	}
	for k, newValue := range override {
		if baseValue, exists := base[k]; exists {
			srcMap, srcMapOk := newValue.(map[string]any)
			dstMap, dstMapOk := baseValue.(map[string]any)
			if srcMapOk && dstMapOk {
				newValue = MergeConfiguration(dstMap, srcMap)
			}
		}
		base[k] = newValue
	}

	return base
}
