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

package amwusage

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLiteBootstrapCrashRecovery(t *testing.T) {
	if path := os.Getenv("AMW_BOOTSTRAP_CRASH_CHILD"); path != "" {
		s, err := OpenScanStore(path)
		if err != nil {
			t.Fatal(err)
		}
		seed := scanSeed(t)
		if err := s.stageBootstrap(context.Background(), strings.NewReader(seed)); err != nil {
			t.Fatal(err)
		}
		if os.Getenv("AMW_BOOTSTRAP_DURING_IMPORT") == "1" {
			tx, err := s.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if err := importScanSeed(context.Background(), tx, strings.NewReader(seed), scanHash(seed)); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`DELETE FROM bootstrap`); err != nil {
				t.Fatal(err)
			}
			// Die with a fully built but uncommitted plan and seed deletion.
		}
		os.Exit(23)
	}
	for _, duringImport := range []string{"0", "1"} {
		t.Run("during_import_"+duringImport, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "scan.db")
			cmd := exec.Command(os.Args[0], "-test.run=^TestSQLiteBootstrapCrashRecovery$")
			cmd.Env = append(os.Environ(), "AMW_BOOTSTRAP_CRASH_CHILD="+path, "AMW_BOOTSTRAP_DURING_IMPORT="+duringImport)
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 23 {
				t.Fatalf("child did not crash as intended: %v %s", err, output)
			}
			s, err := OpenScanStore(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			var complete, runs int
			var fingerprint string
			if err := s.db.QueryRow(`SELECT complete,seed_hash,(SELECT count(*) FROM run) FROM bootstrap`).Scan(&complete, &fingerprint, &runs); err != nil || complete != 1 || runs != 0 || fingerprint != scanHash(scanSeed(t)) {
				t.Fatalf("registered seed did not survive crash: complete=%d runs=%d hash=%s err=%v", complete, runs, fingerprint, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			transport := collectionTestTransport(func(*http.Request) (*http.Response, error) {
				calls++
				cancel()
				return nil, context.Canceled
			})
			if err := s.Scan(ctx, ScanOptions{Workers: 1, Credential: scanTestCredential{}, Transport: transport}); !errors.Is(err, context.Canceled) {
				t.Fatalf("seedless scan did not reach fake HTTP: %v", err)
			}
			if calls != 1 {
				t.Fatalf("HTTP calls: %d", calls)
			}
			assertBootstrapImported(t, s)
		})
	}
}

func assertBootstrapImported(t *testing.T, s *ScanStore) {
	t.Helper()
	summary, err := s.Summary(context.Background())
	if err != nil || summary.PlanningState != "complete" || summary.RankingTotal != 6 || summary.RankingSucceeded != 1 || summary.Observations != 1 {
		t.Fatalf("recovered plan: %+v %v", summary, err)
	}
	var retained int
	if err := s.db.QueryRow(`SELECT (SELECT count(*) FROM bootstrap)+(SELECT count(*) FROM bootstrap_chunk)`).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("temporary seed retained: %d %v", retained, err)
	}
}

func TestSQLiteBootstrapImportFailureAndIncompletePlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scan.db")
	s, err := OpenScanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	seed := scanSeed(t)
	if err := s.stageBootstrap(ctx, strings.NewReader(seed)); err != nil {
		t.Fatal(err)
	}
	if err := s.Initialize(ctx, strings.NewReader(seed+" ")); err == nil || !strings.Contains(err.Error(), "fingerprint differs") {
		t.Fatalf("changed registered seed accepted: %v", err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_plan BEFORE UPDATE OF planning_state ON run BEGIN SELECT RAISE(ABORT,'test import failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.Initialize(ctx, nil); err == nil || !strings.Contains(err.Error(), "test import failure") {
		t.Fatalf("expected import failure: %v", err)
	}
	var runs, complete int
	if err := s.db.QueryRow(`SELECT (SELECT count(*) FROM run),complete FROM bootstrap`).Scan(&runs, &complete); err != nil || runs != 0 || complete != 1 {
		t.Fatalf("import failure lost seed or published partial plan: %d %d %v", runs, complete, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_plan`); err != nil {
		t.Fatal(err)
	}
	// Exercise a persisted incomplete plan, including archived observations and
	// circular query/attempt references, while the registered seed remains.
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := importScanSeed(ctx, tx, strings.NewReader(seed), scanHash(seed)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE run SET planning_state='planning'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenScanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Initialize(ctx, nil); err != nil {
		t.Fatal(err)
	}
	assertBootstrapImported(t, s)
	if err := s.Initialize(ctx, strings.NewReader(seed)); err != nil {
		t.Fatal(err)
	}
	assertBootstrapImported(t, s)
}

type bootstrapReadFailure struct{}

func (bootstrapReadFailure) Read([]byte) (int, error) {
	return 0, errors.New("test interrupted upload")
}

func TestSQLiteBootstrapIncompleteUpload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scan.db")
	s, err := OpenScanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	noise := make([]byte, 3*bootstrapChunkSize)
	if _, err := rand.New(rand.NewSource(1)).Read(noise); err != nil {
		t.Fatal(err)
	}
	if err := s.Initialize(ctx, io.MultiReader(bytes.NewReader(noise), bootstrapReadFailure{})); err == nil || !strings.Contains(err.Error(), "interrupted upload") {
		t.Fatalf("expected failed upload: %v", err)
	}
	var complete, chunks, largest int
	if err := s.db.QueryRow(`SELECT complete,(SELECT count(*) FROM bootstrap_chunk),(SELECT max(length(data)) FROM bootstrap_chunk) FROM bootstrap`).Scan(&complete, &chunks, &largest); err != nil || complete != 0 || chunks < 2 || largest > bootstrapChunkSize {
		t.Fatalf("upload not committed in bounded chunks: complete=%d chunks=%d largest=%d err=%v", complete, chunks, largest, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenScanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	transport := collectionTestTransport(func(*http.Request) (*http.Response, error) {
		t.Error("HTTP attempted before seed registration")
		return nil, errors.New("unexpected HTTP")
	})
	if err := s.Scan(ctx, ScanOptions{Credential: scanTestCredential{}, Transport: transport}); err == nil || !strings.Contains(err.Error(), "registration is missing or incomplete") {
		t.Fatalf("incomplete upload used as seed: %v", err)
	}
	if err := s.Initialize(ctx, strings.NewReader(scanSeed(t))); err != nil {
		t.Fatal(err)
	}
	assertBootstrapImported(t, s)
}

func TestSQLiteBootstrapHashValidation(t *testing.T) {
	s, err := OpenScanStore(filepath.Join(t.TempDir(), "scan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.stageBootstrap(ctx, strings.NewReader(scanSeed(t))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE bootstrap SET seed_hash='wrong'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Initialize(ctx, nil); err == nil || !strings.Contains(err.Error(), "fingerprint differs") {
		t.Fatalf("corrupt bootstrap fingerprint accepted: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE bootstrap SET seed_hash=?`, scanHash(scanSeed(t))); err != nil {
		t.Fatal(err)
	}
	if err := s.Initialize(ctx, nil); err != nil {
		t.Fatal(err)
	}
	assertBootstrapImported(t, s)
}

func TestSQLiteBootstrapChunkReplay(t *testing.T) {
	s, err := OpenScanStore(filepath.Join(t.TempDir(), "scan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	noise := make([]byte, 3*bootstrapChunkSize)
	if _, err := rand.New(rand.NewSource(2)).Read(noise); err != nil {
		t.Fatal(err)
	}
	seed := scanSeed(t)
	seed = seed[:len(seed)-1] + `,"padding":"` + base64.StdEncoding.EncodeToString(noise) + `"}`
	if err := s.stageBootstrap(ctx, strings.NewReader(seed)); err != nil {
		t.Fatal(err)
	}
	var chunks int
	if err := s.db.QueryRow(`SELECT count(*) FROM bootstrap_chunk`).Scan(&chunks); err != nil || chunks < 3 {
		t.Fatalf("expected multi-chunk seed: %d %v", chunks, err)
	}
	// Corruption must not publish a plan or discard the remaining source.
	var chunk []byte
	if err := s.db.QueryRow(`SELECT data FROM bootstrap_chunk WHERE sequence=1`).Scan(&chunk); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE bootstrap_chunk SET data=x'00' WHERE sequence=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.Initialize(ctx, nil); err == nil {
		t.Fatal("corrupt gzip stream accepted")
	}
	if _, err := s.db.Exec(`UPDATE bootstrap_chunk SET data=? WHERE sequence=1`, chunk); err != nil {
		t.Fatal(err)
	}
	if err := s.Initialize(ctx, nil); err != nil {
		t.Fatalf("multi-chunk replay: %v", err)
	}
	assertBootstrapImported(t, s)
}

func TestSQLiteBootstrapLegacyInitialized(t *testing.T) {
	s, _ := newScanTestStore(t)
	if _, err := s.db.Exec(`DROP TABLE bootstrap_chunk; DROP TABLE bootstrap`); err != nil {
		t.Fatal(err)
	}
	if err := s.Initialize(context.Background(), nil); err != nil {
		t.Fatalf("legacy initialized database required bootstrap: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := collectionTestTransport(func(*http.Request) (*http.Response, error) {
		cancel()
		return nil, context.Canceled
	})
	if err := s.Scan(ctx, ScanOptions{Workers: 1, Credential: scanTestCredential{}, Transport: transport}); !errors.Is(err, context.Canceled) {
		t.Fatalf("legacy scan did not reach fake HTTP: %v", err)
	}
	var tables, version int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name LIKE 'bootstrap%'`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("legacy scan created bootstrap tables: %d %v", tables, err)
	}
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 1 {
		t.Fatalf("schema version changed: %d %v", version, err)
	}
}
