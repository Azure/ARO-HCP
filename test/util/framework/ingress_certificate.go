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
	"net/url"
	"os"
	"strings"
)

func IngressCertificateVaultURLs() ([]string, error) {
	configured := strings.TrimSpace(os.Getenv("ARO_HCP_INGRESS_CERTIFICATE_VAULT_URLS"))
	if configured == "" {
		return nil, fmt.Errorf("ARO_HCP_INGRESS_CERTIFICATE_VAULT_URLS must list the OneCert certificate vault HTTPS URLs")
	}
	vaultURLs := strings.Split(configured, ",")
	for index, vaultURL := range vaultURLs {
		vaultURL = strings.TrimSpace(vaultURL)
		parsed, err := url.Parse(vaultURL)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
			return nil, fmt.Errorf("invalid certificate vault URL at index %d: expected an HTTPS vault origin", index)
		}
		vaultURLs[index] = strings.TrimSuffix(vaultURL, "/")
	}
	return vaultURLs, nil
}
