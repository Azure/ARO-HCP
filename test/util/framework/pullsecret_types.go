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

package framework

// RegistryAuth represents authentication credentials for a single registry.
// This type models the structure of dockerconfigjson registry auth entries.
type RegistryAuth struct {
	Username string `json:"username,omitempty"`
	Email    string `json:"email,omitempty"`
	Auth     string `json:"auth"`
}

// DockerConfigJSON is the root structure for dockerconfigjson secret data.
// See: https://kubernetes.io/docs/concepts/configuration/secret/#docker-config-secrets
type DockerConfigJSON struct {
	Auths map[string]RegistryAuth `json:"auths"`
}
