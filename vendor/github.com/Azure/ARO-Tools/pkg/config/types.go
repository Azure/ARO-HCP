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
	"github.com/Azure/ARO-Tools/pkg/config/types"
)

// DEPRECATED: use the exported type from types package instead
type Configuration = types.Configuration

// configurationOverrides is the internal representation for config stored on disk - we do not export it as we
// require that users pre-process it first, which the ConfigProvider.GetResolver() will do for them.
type configurationOverrides struct {
	Schema   string              `json:"$schema"`
	Defaults types.Configuration `json:"defaults"`
	// key is the cloud alias
	Overrides map[string]*struct {
		Defaults types.Configuration `json:"defaults"`
		// key is the deploy env
		Overrides map[string]*struct {
			Defaults types.Configuration `json:"defaults"`
			// key is the region name
			Overrides map[string]types.Configuration `json:"regions"`
		} `json:"environments"`
	} `json:"clouds"`
}
