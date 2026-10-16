// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cost

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func WriteSnapshot(path string, snapshot *Snapshot) error {
	return WriteFile(path, func(w io.Writer) error {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(snapshot)
	})
}

func ReadSnapshot(path string) (*Snapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	var s Snapshot
	if err := decoder.Decode(&s); err != nil {
		return nil, err
	}
	if s.Version != SchemaVersion {
		return nil, fmt.Errorf("unsupported snapshot version %d", s.Version)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing snapshot content")
	}
	return &s, nil
}

// WriteFile replaces an output only after it is fully written. Billing outputs
// are private by default, including when replacing a previously public file.
func WriteFile(path string, write func(io.Writer) error) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".e2e-cost-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := write(f); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
