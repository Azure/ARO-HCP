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

package azsdk

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/internal/version"
)

// Component identifies which ARO-HCP component is making Azure SDK calls.
type Component string

const (
	ComponentFrontend Component = "frontend"
	ComponentBackend  Component = "backend"
	ComponentAdmin    Component = "admin"
)

// NewClientOptions returns azcore.ClientOptions with Telemetry.ApplicationID
// set to identify the ARO-HCP component and its version.
// The ApplicationID follows the format: <component>/<commitSHA> truncating to 24 characters as
// per the Azure SDK guidelines.
func NewClientOptions(component Component) azcore.ClientOptions {
	return azcore.ClientOptions{
		Telemetry: policy.TelemetryOptions{
			ApplicationID: firstN(fmt.Sprintf("%s/%s", component, version.CommitSHA), 24),
		},
	}
}

func firstN(str string, n int) string {
	v := []rune(str)
	if n >= len(v) {
		return str
	}

	return string(v[:n])
}
