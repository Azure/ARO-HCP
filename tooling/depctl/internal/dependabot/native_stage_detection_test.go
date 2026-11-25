// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package dependabot

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// TestStageNativeDetection expands both receipt formats without changing any original artifact or unrelated output.
func TestStageNativeDetection(t *testing.T) {
	for _, nested := range []bool{false, true} {
		name := "flat receipt"
		if nested {
			name = "nested receipt"
		}
		t.Run(name, func(t *testing.T) {
			request := nativePublicationTestRequest()
			request.Title = "chore(deps): update @scope/library café"
			request.Body = "<p>Upgrade @scope/library. Unicode: 🦉.</p>\n"
			request.Patch = strings.Replace(request.Patch, "+new\n", "+new @scope/library café 🦉\r\n", 1)
			payload, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			fixture := writeNativeDetectionFixture(t, payload, nested)
			original := readNativeDetectionTestFile(t, fixture.output)
			ordinaryPatch := filepath.Join(fixture.detection, "aw-ordinary.patch")
			nativeRequestWrite(t, ordinaryPatch, []byte("Ordinary patch\n"))

			result, err := StageNativeDetection(fixture.output, fixture.detection)
			if err != nil || !reflect.DeepEqual(result, map[string]any{"status": "staged", "patch": nativeDetectionPatch}) {
				t.Fatalf("stage detection: got %v, err %v", result, err)
			}
			staged := readNativeDetectionTestFile(t, fixture.prepared)
			wantValue, err := nativeDecode(original)
			if err != nil {
				t.Fatal(err)
			}
			want := wantValue.(map[string]any)
			item := want["items"].([]any)[1].(map[string]any)
			item["title"] = "chore(deps): update ＠scope/library café"
			item["body"] = "<p>Upgrade ＠scope/library. Unicode: 🦉.</p>\n"
			item["source_prs"] = []any{map[string]any{"number": json.Number("7"), "head_sha": request.SourcePRs[0].HeadSHA}}
			item["expected_base_sha"] = request.ExpectedBaseSHA
			got, err := nativeDecode(staged)
			if err != nil || !reflect.DeepEqual(got, want) || !bytes.Contains(staged, []byte("<p>Upgrade ＠scope/library")) {
				t.Fatalf("expanded detector output differs: %s (err %v)", staged, err)
			}
			if patch := readNativeDetectionTestFile(t, fixture.patch); string(patch) != request.Patch {
				t.Fatalf("patch bytes changed: %q", patch)
			}
			if !bytes.Equal(readNativeDetectionTestFile(t, fixture.output), original) || !bytes.Equal(readNativeDetectionTestFile(t, fixture.artifact), payload) || string(readNativeDetectionTestFile(t, ordinaryPatch)) != "Ordinary patch\n" {
				t.Fatal("staging modified original input or unrelated patch")
			}
			publication, err := ReadNativePublicationRequest(fixture.output)
			if err != nil || publication == nil || !reflect.DeepEqual(publication.request, request) {
				t.Fatalf("publication no longer reads original request: %v (err %v)", publication, err)
			}
		})
	}
}

// TestStageNativeDetectionNoRequest leaves prepared artifacts alone and does not require a detector directory.
func TestStageNativeDetectionNoRequest(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "absent detector directory"
		if prepared {
			name = "existing prepared outputs"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			output := filepath.Join(root, nativeDetectionOutput)
			detection := filepath.Join(root, "detection")
			original := []byte(`{"items":[{"type":"noop","message":"No replacement"}]}`)
			ordinary := []byte("Ordinary patch\n")
			nativeRequestWrite(t, output, original)
			if prepared {
				if err := os.Mkdir(detection, 0700); err != nil {
					t.Fatal(err)
				}
				nativeRequestWrite(t, filepath.Join(detection, nativeDetectionOutput), original)
				nativeRequestWrite(t, filepath.Join(detection, "aw-ordinary.patch"), ordinary)
			}
			result, err := StageNativeDetection(output, detection)
			if err != nil || !reflect.DeepEqual(result, map[string]any{"status": "no-request"}) {
				t.Fatalf("stage no request: got %v, err %v", result, err)
			}
			if !bytes.Equal(readNativeDetectionTestFile(t, output), original) {
				t.Fatal("no-request run modified original input")
			}
			if prepared && (!bytes.Equal(readNativeDetectionTestFile(t, filepath.Join(detection, nativeDetectionOutput)), original) || !bytes.Equal(readNativeDetectionTestFile(t, filepath.Join(detection, "aw-ordinary.patch")), ordinary)) {
				t.Fatal("no-request run modified detector input")
			}
			if _, err := os.Lstat(filepath.Join(detection, nativeDetectionPatch)); !os.IsNotExist(err) {
				t.Fatalf("unexpected staged patch: %v", err)
			}
		})
	}
}

// TestStageNativeDetectionRejectsUnsafeArtifacts checks validation before any prepared input is written.
func TestStageNativeDetectionRejectsUnsafeArtifacts(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		payload   []byte
		mutate    func(*testing.T, nativeDetectionTestFixture)
		wantError string
	}{
		{name: "missing artifact", mutate: func(t *testing.T, f nativeDetectionTestFixture) {
			if err := os.Remove(f.artifact); err != nil {
				t.Fatal(err)
			}
		}, wantError: "open native request artifact"},
		{name: "tampered artifact", mutate: func(t *testing.T, f nativeDetectionTestFixture) {
			nativeRequestWrite(t, f.artifact, append(readNativeDetectionTestFile(t, f.artifact), '\n'))
		}, wantError: "digest mismatch"},
		{name: "invalid schema", payload: []byte(`{"patch":"not enough"}`), wantError: "invalid replacement schema"},
		{name: "malformed JSON", payload: []byte(`{`), wantError: "decode native request"},
		{name: "command", body: "Evidence\n/retest", wantError: "publication body"},
		{name: "ownership marker", body: "<!-- native-dependabot-maintenance:spoof -->", wantError: "publication body"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := test.payload
			if payload == nil {
				request := nativePublicationTestRequest()
				if test.body != "" {
					request.Body = test.body
				}
				var err error
				payload, err = json.Marshal(request)
				if err != nil {
					t.Fatal(err)
				}
			}
			f := writeNativeDetectionFixture(t, payload, false)
			if test.mutate != nil {
				test.mutate(t, f)
			}
			before := readNativeDetectionTestFile(t, f.prepared)
			ordinary := filepath.Join(f.detection, "aw-ordinary.patch")
			nativeRequestWrite(t, ordinary, []byte("Ordinary patch\n"))
			if result, err := StageNativeDetection(f.output, f.detection); result != nil || err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("got result %v and error %v; want %q", result, err, test.wantError)
			}
			if !bytes.Equal(readNativeDetectionTestFile(t, f.prepared), before) || string(readNativeDetectionTestFile(t, ordinary)) != "Ordinary patch\n" {
				t.Fatal("validation failure modified prepared detector inputs")
			}
			if _, err := os.Lstat(f.patch); !os.IsNotExist(err) {
				t.Fatalf("invalid request created detector patch: %v", err)
			}
		})
	}
}

// TestStageNativeDetectionProtectsFiles rejects aliases, nonregular entries, a mismatched copy and patch collisions.
func TestStageNativeDetectionProtectsFiles(t *testing.T) {
	for _, test := range []struct {
		name      string
		entry     string
		kind      string
		wantError string
	}{
		{name: "same file", kind: "same", wantError: "separate copy"},
		{name: "mismatched copy", entry: "prepared", kind: "mismatch", wantError: "does not match"},
		{name: "prepared symlink", entry: "prepared", kind: "symlink", wantError: "open prepared detector output"},
		{name: "prepared hardlink", entry: "prepared", kind: "hardlink", wantError: "singly linked regular file"},
		{name: "prepared FIFO", entry: "prepared", kind: "fifo", wantError: "singly linked regular file"},
		{name: "original symlink", entry: "original", kind: "symlink", wantError: "open native output"},
		{name: "original hardlink", entry: "original", kind: "hardlink", wantError: "singly linked regular file"},
		{name: "artifact symlink", entry: "artifact", kind: "symlink", wantError: "open native request artifact"},
		{name: "artifact hardlink", entry: "artifact", kind: "hardlink", wantError: "singly linked regular file"},
		{name: "artifact FIFO", entry: "artifact", kind: "fifo", wantError: "singly linked regular file"},
		{name: "detector directory symlink", entry: "directory", kind: "symlink", wantError: "open detector directory"},
		{name: "preexisting patch", entry: "patch", kind: "regular", wantError: "create native detector patch"},
		{name: "patch symlink", entry: "patch", kind: "symlink", wantError: "create native detector patch"},
		{name: "patch hardlink", entry: "patch", kind: "hardlink", wantError: "create native detector patch"},
		{name: "patch FIFO", entry: "patch", kind: "fifo", wantError: "create native detector patch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload, err := json.Marshal(nativePublicationTestRequest())
			if err != nil {
				t.Fatal(err)
			}
			f := writeNativeDetectionFixture(t, payload, false)
			root := filepath.Dir(f.output)
			outside := filepath.Join(root, "untouched")
			original := readNativeDetectionTestFile(t, f.output)
			prepared := readNativeDetectionTestFile(t, f.prepared)
			detection := f.detection
			path := map[string]string{"prepared": f.prepared, "original": f.output, "artifact": f.artifact, "directory": f.detection, "patch": f.patch}[test.entry]
			var target []byte
			switch test.entry {
			case "prepared":
				target = prepared
			case "original":
				target = original
			case "artifact":
				target = payload
			case "patch":
				target = []byte("Do not overwrite\n")
			}
			if test.kind == "symlink" || test.kind == "hardlink" || test.kind == "fifo" {
				if test.entry != "patch" && test.entry != "directory" {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
				if target != nil {
					nativeRequestWrite(t, outside, target)
				}
			}
			switch test.kind {
			case "same":
				detection = root
			case "mismatch":
				prepared = []byte(`{"items":[]}`)
				nativeRequestWrite(t, path, prepared)
			case "regular":
				nativeRequestWrite(t, path, target)
			case "symlink":
				if test.entry == "directory" {
					detection = outside + string(os.PathSeparator)
					outside, path = f.detection, outside
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(outside, path); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := unix.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if result, err := StageNativeDetection(f.output, detection); result != nil || err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("got result %v and error %v; want %q", result, err, test.wantError)
			}
			if (test.kind == "symlink" || test.kind == "hardlink") && target != nil && !bytes.Equal(readNativeDetectionTestFile(t, outside), target) {
				t.Fatal("staging followed a link and modified its target")
			}
			if test.kind == "fifo" {
				if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
					t.Fatalf("FIFO was replaced: %v", err)
				}
			}
			if test.entry != "prepared" || test.kind == "mismatch" {
				if !bytes.Equal(readNativeDetectionTestFile(t, f.prepared), prepared) {
					t.Fatal("rejected staging modified prepared detector output")
				}
			}
			if test.entry != "original" && !bytes.Equal(readNativeDetectionTestFile(t, f.output), original) {
				t.Fatal("rejected staging modified original output")
			}
			if test.entry != "patch" {
				if _, err := os.Lstat(filepath.Join(detection, nativeDetectionPatch)); !os.IsNotExist(err) {
					t.Fatalf("rejected staging created patch: %v", err)
				}
			}
			if test.kind == "regular" && !bytes.Equal(readNativeDetectionTestFile(t, path), target) {
				t.Fatal("staging overwrote the preexisting patch")
			}
		})
	}
}

// Native detector test fixtures hold the publication transport and its independent prepared copy.
type nativeDetectionTestFixture struct {
	output, detection, prepared, artifact, patch string
}

// Write a detector fixture with the same envelope in two different JSON representations.
func writeNativeDetectionFixture(t *testing.T, payload []byte, nested bool) nativeDetectionTestFixture {
	t.Helper()
	root := t.TempDir()
	detection := filepath.Join(root, "detection")
	agent := filepath.Join(root, "agent")
	for _, directory := range []string{agent, detection} {
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	f := nativeDetectionTestFixture{
		output:    filepath.Join(root, nativeDetectionOutput),
		detection: detection,
		prepared:  filepath.Join(detection, nativeDetectionOutput),
		artifact:  filepath.Join(agent, nativeRequestFile),
		patch:     filepath.Join(detection, nativeDetectionPatch),
	}
	item := map[string]any{"type": "native_replacement", "output_id": "so_WEOMbF-MTV-Q"}
	// Populate the digest before adding the unrelated output, as the fixture helper targets item zero.
	envelope := map[string]any{"items": []any{item}, "errors": []any{}, "trace": "keep top-level metadata"}
	nativePublicationTestDigest(payload, envelope)
	if nested {
		item["data"] = map[string]any{"request_sha256": item["request_sha256"]}
		delete(item, "request_sha256")
	}
	envelope["items"] = []any{map[string]any{"type": "noop", "message": "Keep unrelated outputs", "sequence": json.Number("9007199254740999")}, item}
	original, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	nativeRequestWrite(t, f.output, original)
	nativeRequestWrite(t, f.prepared, prepared)
	nativeRequestWrite(t, f.artifact, payload)
	return f
}

// Read a regular detector test file without hiding failures.
func readNativeDetectionTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
