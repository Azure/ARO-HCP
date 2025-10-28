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

// code copied from https://github.com/openshift/must-gather-clean/tree/main/pkg/schema
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
