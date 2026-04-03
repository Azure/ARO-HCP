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

package arm

import (
	"fmt"
	"net/url"
	"strings"
)

// allowedNotificationHosts lists known Azure Resource Manager management
// endpoints that are permitted as notification callback targets.
// https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/control-plane-and-data-plane#control-plane
var allowedNotificationHosts = []string{
	"management.azure.com",
	"management.usgovcloudapi.net",
}

// ValidateNotificationURI validates that an Azure-AsyncNotificationUri
// header value is safe to use as a callback target. An empty string is
// considered valid (no notification). A non-empty URI must be HTTPS and
// target a known ARM management endpoint to prevent SSRF attacks.
func ValidateNotificationURI(uri string) error {
	if uri == "" {
		return nil
	}

	parsed, err := url.Parse(uri)
	if err != nil {
		return fmt.Errorf("invalid notification URI: %w", err)
	}

	if !strings.EqualFold(parsed.Scheme, "https") {
		return fmt.Errorf("notification URI scheme must be HTTPS, got %q", parsed.Scheme)
	}

	host := strings.ToLower(parsed.Hostname())
	for _, allowed := range allowedNotificationHosts {
		if host == allowed {
			return nil
		}
	}

	return fmt.Errorf("notification URI host %q is not a known ARM management endpoint", host)
}
