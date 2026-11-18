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

package leaseenv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

const MockIdentityBundle = "LEASED_MOCK_IDENTITY_BUNDLE"

type MockPrincipal struct {
	ClientID        string `json:"clientId" yaml:"client_id"`
	PrincipalID     string `json:"principalId" yaml:"principal_id"`
	CertificateName string `json:"certName" yaml:"certificate_name"`
}

type MockIdentityBundlePayload struct {
	SlotName        string        `json:"slotName" yaml:"slot_name"`
	SubscriptionID  string        `json:"subscriptionId" yaml:"subscription_id"`
	MSI             MockPrincipal `json:"msi" yaml:"msi"`
	Backend         MockPrincipal `json:"backend" yaml:"backend"`
	ClustersService MockPrincipal `json:"clustersService" yaml:"clusters_service"`
}

var certificateName = regexp.MustCompile(`^[a-zA-Z0-9-]{1,127}$`)

func (bundle MockIdentityBundlePayload) Validate() error {
	if bundle.SlotName == "" || strings.TrimSpace(bundle.SlotName) != bundle.SlotName {
		return fmt.Errorf("mock identity bundle requires slotName")
	}
	if subscriptionID, err := uuid.Parse(bundle.SubscriptionID); err != nil || subscriptionID == uuid.Nil {
		return fmt.Errorf("mock identity bundle requires subscription UUID")
	}
	ids := map[uuid.UUID]bool{}
	certificates := map[string]bool{}
	for _, principal := range []MockPrincipal{bundle.MSI, bundle.Backend, bundle.ClustersService} {
		for _, value := range []string{principal.ClientID, principal.PrincipalID} {
			id, err := uuid.Parse(value)
			if err != nil || id == uuid.Nil || ids[id] {
				return fmt.Errorf("mock identities require distinct nonzero client and principal UUIDs")
			}
			ids[id] = true
		}
		if !certificateName.MatchString(principal.CertificateName) || certificates[principal.CertificateName] {
			return fmt.Errorf("mock identities require distinct valid certificate names")
		}
		certificates[principal.CertificateName] = true
	}
	return nil
}

func ParseMockIdentityBundle(payload string) (MockIdentityBundlePayload, error) {
	var bundle MockIdentityBundlePayload
	decoder := json.NewDecoder(bytes.NewBufferString(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return bundle, fmt.Errorf("%s: %w", MockIdentityBundle, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return bundle, fmt.Errorf("%s must contain exactly one JSON object", MockIdentityBundle)
	}
	return bundle, bundle.Validate()
}
