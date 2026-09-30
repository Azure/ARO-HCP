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

type CustomerSubscription struct {
	Name              string
	ID                string
	ClusterProfileDir string
}

// ResolveCustomerSubscription matches a slot subscription to exactly one
// customer-*-subscription-name/id pair across the supplied cluster profiles.
// The owning profile directory supplies the tenant and service-principal
// credentials for cross-tenant jobs.
func ResolveCustomerSubscription(clusterProfileDirs []string, slotSubscriptionName string) (*CustomerSubscription, error) {
	if len(clusterProfileDirs) == 0 {
		return nil, errors.New("cluster profile dirs are empty")
	}

	if slotSubscriptionName == "" {
		return nil, errors.New("slot subscription name is empty")
	}

	var matchedFile string
	var matchedDir string
	for _, clusterProfileDir := range clusterProfileDirs {
		if clusterProfileDir == "" {
			return nil, errors.New("cluster profile dir is empty")
		}

		entries, err := os.ReadDir(clusterProfileDir)
		if err != nil {
			return nil, fmt.Errorf("failed to read cluster profile dir %q: %w", clusterProfileDir, err)
		}

		for _, entry := range entries {
			if entry.IsDir() || !isCustomerSubscriptionNameFile(entry.Name()) {
				continue
			}

			candidatePath := filepath.Join(clusterProfileDir, entry.Name())
			data, err := os.ReadFile(candidatePath)
			if err != nil {
				return nil, fmt.Errorf("failed to read customer subscription name %q: %w", candidatePath, err)
			}

			if strings.TrimSpace(string(data)) != slotSubscriptionName {
				continue
			}

			if matchedFile != "" {
				return nil, fmt.Errorf(
					"multiple customer subscription name files matched slot subscription %q: %s, %s",
					slotSubscriptionName,
					matchedFile,
					candidatePath,
				)
			}

			matchedFile = candidatePath
			matchedDir = clusterProfileDir
		}
	}

	if matchedFile == "" {
		return nil, fmt.Errorf("no customer subscription name file matched slot subscription %q in %s", slotSubscriptionName, strings.Join(clusterProfileDirs, ", "))
	}

	idFile := strings.TrimSuffix(matchedFile, "-subscription-name") + "-subscription-id"
	idData, err := os.ReadFile(idFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read customer subscription ID %q: %w", idFile, err)
	}
	subscriptionID := strings.TrimSpace(string(idData))
	if subscriptionID == "" {
		return nil, fmt.Errorf("customer subscription ID file %q is empty", idFile)
	}

	return &CustomerSubscription{
		Name:              slotSubscriptionName,
		ID:                subscriptionID,
		ClusterProfileDir: matchedDir,
	}, nil
}

func isCustomerSubscriptionNameFile(name string) bool {
	return strings.HasPrefix(name, "customer-") && strings.HasSuffix(name, "-subscription-name")
}
