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
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	s := &Snapshot{Version: SchemaVersion, Currency: "USD", CostBasis: "AmortizedCost"}
	s.Diagnose("error", "test", "job", "Partial collection")
	if err := WriteSnapshot(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSnapshot(path)
	if err != nil || !got.HasErrors() {
		t.Fatalf("read partial snapshot: %v, %+v", err, got)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("snapshot must be private: %v, %v", info, err)
	}
	wantErr := errors.New("render failed")
	if err := WriteFile(path, func(w io.Writer) error {
		_, _ = io.WriteString(w, "partial")
		return wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("expected write error, got %v", err)
	}
	if _, err := ReadSnapshot(path); err != nil {
		t.Fatalf("failed write destroyed existing snapshot: %v", err)
	}
	for _, body := range []string{`{"version":999}`, `{"version":1,"unknown":true}`, `{"version":1} {}`} {
		if err := WriteFile(path, func(w io.Writer) error { _, err := io.WriteString(w, body); return err }); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadSnapshot(path); err == nil {
			t.Fatalf("accepted invalid snapshot %s", body)
		}
	}
}
