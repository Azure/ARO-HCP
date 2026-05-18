// Copyright 2026 Microsoft Corporation
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

import (
	"fmt"
	"os"
	"strings"
)

const hcpAPIVersionEnvVar = "ARO_HCP_E2E_API_VERSION"

type HCPAPIVersion string

const (
	HCPAPIVersion20240610 HCPAPIVersion = "v20240610preview"
	HCPAPIVersion20251223 HCPAPIVersion = "v20251223preview"

	DefaultHCPAPIVersion = HCPAPIVersion20240610
)

func ParseHCPAPIVersion(raw string) (HCPAPIVersion, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case "":
		return DefaultHCPAPIVersion, nil
	case "20240610", "v20240610", "20240610preview", string(HCPAPIVersion20240610):
		return HCPAPIVersion20240610, nil
	case "20251223", "v20251223", "20251223preview", string(HCPAPIVersion20251223):
		return HCPAPIVersion20251223, nil
	default:
		return "", fmt.Errorf("invalid HCP API version %q, supported values: %s, %s", raw, HCPAPIVersion20240610, HCPAPIVersion20251223)
	}
}

func configuredHCPAPIVersion() (HCPAPIVersion, error) {
	return ParseHCPAPIVersion(os.Getenv(hcpAPIVersionEnvVar))
}
