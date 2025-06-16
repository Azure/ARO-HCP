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

package validate

import (
	"os"

	"sigs.k8s.io/yaml"
)

type Digests struct {
	Clouds map[string]CloudDigests `json:"clouds"`
}

type CloudDigests struct {
	Environments map[string]EnvironmentDigests `json:"environments"`
}

type EnvironmentDigests struct {
	Regions map[string]string `json:"regions"`
}

func LoadDigests(path string) (*Digests, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var out Digests
	return &out, yaml.Unmarshal(raw, &out)
}
