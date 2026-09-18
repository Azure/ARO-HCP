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

package coreapi

import (
	"fmt"
	"net/url"
	"strings"
)

// ParseKeyEncryptionKeyURL parses a Key Vault or Managed HSM key URL and returns
// the vault name, key name, and key version. The URL must use HTTPS and have a
// hostname ending in .vault.azure.net or .managedhsm.azure.net, with a path
// of the form /keys/{name}/{version}.
func ParseKeyEncryptionKeyURL(keyURL string) (vaultName, keyName, version string, err error) {
	u, err := url.Parse(keyURL)
	if err != nil {
		return "", "", "", fmt.Errorf("invalid keyEncryptionKeyUrl: %w", err)
	}
	if u.Scheme != "https" {
		return "", "", "", fmt.Errorf("invalid keyEncryptionKeyUrl: scheme must be https, got %q", u.Scheme)
	}
	hostname := u.Hostname()
	if !strings.HasSuffix(hostname, ".vault.azure.net") && !strings.HasSuffix(hostname, ".managedhsm.azure.net") {
		return "", "", "", fmt.Errorf("invalid keyEncryptionKeyUrl: host must end in .vault.azure.net or .managedhsm.azure.net, got %q", hostname)
	}
	if idx := strings.IndexByte(hostname, '.'); idx > 0 {
		vaultName = hostname[:idx]
	} else {
		return "", "", "", fmt.Errorf("invalid keyEncryptionKeyUrl: cannot extract vault name from host %q", hostname)
	}
	if vaultName == "" {
		return "", "", "", fmt.Errorf("invalid keyEncryptionKeyUrl: vault name is empty")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "keys" || parts[1] == "" || parts[2] == "" {
		return "", "", "", fmt.Errorf("invalid keyEncryptionKeyUrl: path must be /keys/{name}/{version}")
	}
	keyName = parts[1]
	version = parts[2]
	return vaultName, keyName, version, nil
}
