package output

import (
	"encoding/json"

	"gopkg.in/yaml.v2"
)

func PrettyPrintJSON(v interface{}) (string, error) {
	jsonData, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(jsonData), nil
}

func PrettyPrintYAML(v interface{}) (string, error) {
	yamlData, err := yaml.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(yamlData), nil
}
