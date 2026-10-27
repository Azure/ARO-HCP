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

package policy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"sigs.k8s.io/yaml"
)

// Load reads and strictly parses a policy file from disk. Unknown keys are
// rejected so a misspelled setting cannot be silently ignored.
func Load(path string) (*Policy, error) {
	if path == "" {
		return nil, fmt.Errorf("policy path is required")
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read policy file %q: %w", path, err)
	}

	var out Policy
	if err := yaml.UnmarshalStrict(content, &out); err != nil {
		return nil, fmt.Errorf("failed to parse policy file %q: %w", path, err)
	}
	return &out, nil
}

// LoadCertificates reads, strictly parses and validates a certificate cleanup
// policy file. Unknown keys are rejected so a misspelled safeguard cannot be
// silently ignored.
func LoadCertificates(path string) (*CertificatePolicy, error) {
	if path == "" {
		return nil, fmt.Errorf("policy path is required")
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read policy file %q: %w", path, err)
	}

	var document struct {
		Certificates CertificatePolicy `json:"certificates"`
	}
	if err := yaml.UnmarshalStrict(content, &document); err != nil {
		return nil, fmt.Errorf("failed to parse policy file %q: %w", path, err)
	}
	if err := document.Certificates.Validate("certificates"); err != nil {
		return nil, fmt.Errorf("invalid policy file %q: %w", path, err)
	}
	return &document.Certificates, nil
}

// unmarshalStrict rejects unknown fields. Custom UnmarshalJSON methods use it
// because the enclosing strict YAML decoder does not reach into them.
func unmarshalStrict(data []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(out)
}
