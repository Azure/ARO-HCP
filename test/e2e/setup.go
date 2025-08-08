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

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	. "github.com/onsi/ginkgo/v2"

	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/integration"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/log"
)

var (
	e2eSetup integration.SetupModel
)

// systemDataPolicy adds the X-Ms-Arm-Resource-System-Data header to cluster creation PUT requests.
type systemDataPolicy struct{}

func (p *systemDataPolicy) Do(req *policy.Request) (*http.Response, error) {
	// This policy should only apply to the PUT request that creates a cluster.
	if req.Raw().Method == http.MethodPut && strings.Contains(req.Raw().URL.Path, "/hcpOpenShiftClusters/") {
		createdBy := "shadownman@example.com"
		createdByType := api.CreatedByTypeUser
		createdAt := time.Now()
		systemData := &api.SystemData{
			CreatedBy:     &createdBy,
			CreatedByType: &createdByType,
			CreatedAt:     &createdAt,
		}
		systemDataBytes, err := json.Marshal(systemData)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal systemData for header: %w", err)
		}
		req.Raw().Header.Set("X-Ms-Arm-Resource-System-Data", string(systemDataBytes))
	}
	return req.Next()
}

// identityURLPolicy adds the X-Ms-Identity-Url header to simulate ARM.
type identityURLPolicy struct{}

func (p *identityURLPolicy) Do(req *policy.Request) (*http.Response, error) {
	// This header is needed for requests directly against the frontend.
	// The value can be a dummy value for local development.
	req.Raw().Header.Set("X-Ms-Identity-Url", "https://dummyhost.identity.azure.net")
	return req.Next()
}

func setup(ctx context.Context) error {
	// Use GinkgoLabelFilter to check for the 'requirenothing' label
	labelFilter := GinkgoLabelFilter()
	if labels.RequireNothing.MatchesLabelFilter(labelFilter) {
		// Skip loading the e2esetup file
		e2eSetup = integration.SetupModel{} // zero value
	} else {
		var err error
		e2eSetup, err = integration.LoadE2ESetupFile(os.Getenv("SETUP_FILEPATH"))
		if err != nil {
			if bicepName, found := os.LookupEnv("FALLBACK_TO_BICEP"); found {
				// Fallback: create a complete HCP cluster using bicep
				log.Logger.Warnf("Failed to load e2e setup file: %v. Falling back to bicep deployment.", err)
				e2eSetup, err = integration.FallbackCreateClusterWithBicep(ctx, bicepName)
				if err != nil {
					return fmt.Errorf("failed to create cluster with bicep fallback: %w", err)
				}
			} else {
				return fmt.Errorf("failed to load e2e setup file and FALLBACK_TO_BICEP is not set: %w", err)
			}
		}
	}

	return nil
}
