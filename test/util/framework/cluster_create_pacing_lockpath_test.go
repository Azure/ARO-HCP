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

package framework

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestNewClusterCreatePacerScopesLockToArtifactDir pins where the pacer keeps
// its files: both the state file and the lock that guards it live under
// ARTIFACT_DIR, so the lock's scope matches the state's. The lock previously
// lived in os.TempDir(), which every run on the host shares — two runs with
// different ARTIFACT_DIRs would have serialized on one another's lock.
func TestNewClusterCreatePacerScopesLockToArtifactDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ARTIFACT_DIR", dir)

	pacer := newClusterCreatePacer(clusterCreatePacing{max: 12, window: 6 * time.Minute})

	if got, want := pacer.statePath, filepath.Join(dir, clusterCreatePacingStateFile); got != want {
		t.Errorf("statePath = %q, want %q", got, want)
	}
	if got, want := pacer.lockPath, filepath.Join(dir, clusterCreatePacingLockFile); got != want {
		t.Errorf("lockPath = %q, want %q (scoped to ARTIFACT_DIR)", got, want)
	}
	if lockDir, hostTempDir := filepath.Dir(pacer.lockPath), os.TempDir(); lockDir == hostTempDir {
		t.Errorf("lockPath %q is in the host temp dir; runs with different ARTIFACT_DIRs would share it", pacer.lockPath)
	}
}
