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

package acrauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// authFileMode keeps the credential readable only by its owner.
const authFileMode fs.FileMode = 0o600

// UpsertCredential adds or replaces the entry for one registry in a container auth file,
// leaving every other registry untouched. Fields we do not model are preserved, except
// identitytoken on the entry being replaced.
func UpsertCredential(path, registry, username, password string) error {
	if registry == "" {
		return errors.New("registry must not be empty")
	}

	document, err := readAuthFile(path)
	if err != nil {
		return err
	}

	auths, ok := document["auths"].(map[string]any)
	if !ok {
		auths = map[string]any{}
	}

	entry, ok := auths[registry].(map[string]any)
	if !ok {
		entry = map[string]any{}
	}
	entry["auth"] = base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	// containers/image prefers a non-empty identitytoken over auth, so leaving a stale one
	// behind would shadow the credential written above.
	delete(entry, "identitytoken")

	auths[registry] = entry
	document["auths"] = auths

	return writeAuthFile(path, document)
}

func readAuthFile(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read auth file %q: %w", path, err)
	}
	if len(raw) == 0 {
		return map[string]any{}, nil
	}

	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("failed to parse auth file %q: %w", path, err)
	}
	if document == nil {
		document = map[string]any{}
	}
	return document, nil
}

// writeAuthFile writes through a temporary file in the same directory so a failure part-way
// through cannot leave the tool holding a truncated credential file. A bind-mounted auth file
// cannot be renamed over, so that path falls back to a non-atomic in-place write.
func writeAuthFile(path string, document map[string]any) error {
	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize auth file: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory %q: %w", dir, err)
	}

	// CreateTemp opens with mode 0600, so the credential is never briefly world-readable.
	tmp, err := os.CreateTemp(dir, ".auth-*.json")
	if err != nil {
		return fmt.Errorf("failed to create temporary auth file: %w", err)
	}
	defer func() {
		_ = os.Remove(tmp.Name())
	}()

	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write temporary auth file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temporary auth file: %w", err)
	}

	if err := os.Rename(tmp.Name(), path); err != nil {
		// A bind-mounted auth file cannot be renamed over (EBUSY), so write through it instead.
		// Restrict first: WriteFile only applies its mode when it creates the file, so writing
		// into an existing world-readable one would expose the credential before we could chmod.
		if chmodErr := os.Chmod(path, authFileMode); chmodErr != nil && !errors.Is(chmodErr, fs.ErrNotExist) {
			return fmt.Errorf("failed to replace auth file %q: %w: failed to restrict permissions: %w", path, err, chmodErr)
		}
		if fallbackErr := os.WriteFile(path, raw, authFileMode); fallbackErr != nil {
			return fmt.Errorf("failed to replace auth file %q: %w: in-place write also failed: %w", path, err, fallbackErr)
		}
	}
	return nil
}
