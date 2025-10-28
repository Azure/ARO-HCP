package schema

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

const jsonExtension = ".json"
const yamlLongExtension = ".yaml"
const yamlShortExtension = ".yml"

var supportedExtensions = []string{jsonExtension, yamlLongExtension, yamlShortExtension}

type UnsupportedFileTypeError struct {
	UsedExtension       string
	SupportedExtensions []string
}

func (u UnsupportedFileTypeError) Error() string {
	return fmt.Sprintf("unsupported extension \"%s\" found. Only [%s] are supported", u.UsedExtension, strings.Join(u.SupportedExtensions, ","))
}

func ReadConfigFromPath(path string) (*SchemaJson, error) {
	extension := filepath.Ext(path)
	isYaml := isYamlExtension(extension)
	if extension != jsonExtension && !isYaml {
		return nil, wrapError(UnsupportedFileTypeError{
			UsedExtension:       extension,
			SupportedExtensions: supportedExtensions,
		})
	}

	var bytes []byte
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, wrapError(err)
	}

	if isYaml {
		bytes, err = yaml.YAMLToJSON(bytes)
		if err != nil {
			return nil, wrapError(err)
		}
	}

	schema := &SchemaJson{}
	err = schema.UnmarshalJSON(bytes)
	if err != nil {
		return nil, wrapError(err)
	}

	return schema, nil
}

func isYamlExtension(extension string) bool {
	return extension == yamlLongExtension || extension == yamlShortExtension
}

func wrapError(err error) error {
	return fmt.Errorf("config-read: %w", err)
}
