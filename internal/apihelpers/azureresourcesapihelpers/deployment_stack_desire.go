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

package azureresourcesapihelpers

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
)

// DeploymentStackDesireSpecHash hashes the whole spec after canonicalizing JSON object
// keys and whitespace. Array order and JSON number representations are retained.
func DeploymentStackDesireSpecHash(spec azureresourcesapi.DeploymentStackDesireSpec) (string, error) {
	data, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("marshal deployment stack desire spec: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	// Decodes numbers as json.Number instead of float64, preserving exact digits
	// through the decode/re-marshal round trip. Using float64 would lose precision on
	// large integers and could reformat numbers, making the hash unstable.
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("decode deployment stack desire spec: %w", err)
	}
	// In Go's json/v1 keys are stored in deterministic order. In json/v2 keys are marshaled in a non deterministic order by default.
	// If this code is migrated to use to json/v2, we must specify the https://pkg.go.dev/encoding/json/v2#Deterministic option to make it deterministic.
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("canonicalize deployment stack desire spec: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(canonical)), nil
}
