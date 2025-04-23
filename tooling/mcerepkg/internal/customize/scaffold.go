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

package customize

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func LoadScaffoldTemplates(scaffoldDir string) ([]unstructured.Unstructured, error) {
	var manifests []unstructured.Unstructured
	err := filepath.Walk(scaffoldDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && (filepath.Ext(path) == ".yaml" || filepath.Ext(path) == ".yml") {
			fileContent, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			mapContent := make(map[string]interface{})
			err = yaml.Unmarshal(fileContent, &mapContent)
			if err != nil {
				return err
			}

			manifests = append(manifests, convertMapToUnstructured(mapContent))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return manifests, nil
}
