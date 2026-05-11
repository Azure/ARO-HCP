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

type Component string

const (
	ComponentFrontend        Component = "frontend"
	ComponentBackend         Component = "backend"
	ComponentAdmin           Component = "admin"
	ComponentResourceCleaner Component = "resource-cleaner"
	ComponentE2E             Component = "e2e"
)

// ApplicationID returns a telemetry application ID for the given component,
// truncated to 24 characters per Azure SDK guidelines.
func ApplicationID(component Component) string {
	return firstN(fmt.Sprintf("%s/%s", component, version.CommitSHA), 24)
}

// NewClientOptions returns azcore.ClientOptions with Telemetry.ApplicationID
// set to identify the ARO-HCP component and its version.
func NewClientOptions(component Component) azcore.ClientOptions {
	return azcore.ClientOptions{
		Telemetry: policy.TelemetryOptions{
			ApplicationID: ApplicationID(component),
		},
	}
}

func firstN(str string, n int) string {
	if n <= 0 {
		return ""
	}
	v := []rune(str)
	if n >= len(v) {
		return str
	}
	return string(v[:n])
}
