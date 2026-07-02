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

package ocm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// canonicalJSONForUpdateDispatchConfig returns canonical JSON for hashing any
// update-dispatch config struct. The value is marshaled first so json tags and
// omitempty apply, then round-tripped through map[string]any so object keys
// are emitted in sorted order at every level.
func canonicalJSONForUpdateDispatchConfig(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to marshal update dispatch config: %w", err))
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to unmarshal update dispatch config: %w", err))
	}

	raw, err = json.Marshal(payload)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to marshal update dispatch config payload: %w", err))
	}
	return raw, nil
}

// hashUpdateDispatchConfig takes any update-dispatch config, it converts it to
// its canonical JSON form and then hashes it using SHA-256 sum encoded to a
// hex string.
func hashUpdateDispatchConfig(v any) (string, error) {
	raw, err := canonicalJSONForUpdateDispatchConfig(v)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
