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

package slots

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// VerifyCustomerSubscriptionName checks that slotSubscriptionName matches
// exactly one customer-*-subscription-name file inside clusterProfileDir.
// It returns the validated name (not a subscription ID) because downstream
// E2E steps expect the human-readable subscription name.
func VerifyCustomerSubscriptionName(clusterProfileDir, slotSubscriptionName string) (string, error) {
	clusterProfileDir = strings.TrimSpace(clusterProfileDir)
	if clusterProfileDir == "" {
		return "", errors.New("cluster profile dir is empty")
	}

	slotSubscriptionName = strings.TrimSpace(slotSubscriptionName)
	if slotSubscriptionName == "" {
		return "", errors.New("slot subscription name is empty")
	}

	entries, err := os.ReadDir(clusterProfileDir)
	if err != nil {
		return "", fmt.Errorf("failed to read cluster profile dir %q: %w", clusterProfileDir, err)
	}

	var matchedFile string
	for _, entry := range entries {
		if entry.IsDir() || !isCustomerSubscriptionNameFile(entry.Name()) {
			continue
		}

		candidatePath := filepath.Join(clusterProfileDir, entry.Name())
		data, err := os.ReadFile(candidatePath)
		if err != nil {
			return "", fmt.Errorf("failed to read customer subscription name %q: %w", candidatePath, err)
		}

		if strings.TrimSpace(string(data)) != slotSubscriptionName {
			continue
		}

		if matchedFile != "" {
			return "", fmt.Errorf(
				"multiple customer subscription name files matched slot subscription %q: %s, %s",
				slotSubscriptionName,
				matchedFile,
				candidatePath,
			)
		}

		matchedFile = candidatePath
	}

	if matchedFile == "" {
		return "", fmt.Errorf("no customer subscription name file matched slot subscription %q in %q", slotSubscriptionName, clusterProfileDir)
	}

	return slotSubscriptionName, nil
}

func isCustomerSubscriptionNameFile(name string) bool {
	return strings.HasPrefix(name, "customer-") && strings.HasSuffix(name, "-subscription-name")
}
