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

package v20240610preview

import (
	"fmt"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api"
)

type version struct {
}

func NewVersion() version {
	return version{}
}

// String returns the api-version parameter value for this API.
func (v version) String() string {
	return "2024-06-10-preview"
}

func (v version) ValidationPathRewriter(internalObj any) (api.ValidationPathMapperFunc, error) {
	switch internalObj.(type) {
	case *api.NodePool:
		return nil, nil
	case *api.ExternalAuth:
		return nil, nil
	case *api.Cluster:
		return propertiesReplacer.Replace, nil

	default:
		return nil, fmt.Errorf("unexpected type %T", internalObj)
	}
}

var (
	versionedInterface = NewVersion()
	propertiesReplacer = strings.NewReplacer("customerProperties", "properties", "serviceProviderProperties", "properties")
)

func RegisterVersion(apiRegistry api.APIRegistry) error {
	if err := apiRegistry.Register(versionedInterface); err != nil {
		return err
	}
	return nil
}
