package utils

import (
	"fmt"
)

// ParseStringField safely extracts a string field from a map[string]interface{}
func ParseStringField(data map[string]interface{}, field string) string {
	if value, ok := data[field].(string); ok {
		return value
	}
	return ""
}

// ParseIntField safely extracts an int field from a map[string]interface{}
func ParseIntField(data map[string]interface{}, field string) *int {
	if value, ok := data[field].(int); ok {
		return &value
	}
	return nil
}

// ParseResourceGraphResultData converts Resource Graph result data to a slice of maps
func ParseResourceGraphResultData(data interface{}) ([]map[string]interface{}, error) {
	if data == nil {
		return nil, nil
	}

	rows, ok := data.([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected Resource Graph response format: expected []interface{}, got %T", data)
	}

	var results []map[string]interface{}
	for _, row := range rows {
		rowMap, ok := row.(map[string]interface{})
		if !ok {
			continue // Skip invalid rows
		}
		results = append(results, rowMap)
	}

	return results, nil
}
