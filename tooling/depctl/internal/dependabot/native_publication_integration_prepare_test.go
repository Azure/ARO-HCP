// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package dependabot

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestNativePublicationIntegrationPreparedArtifact carries committed, staged and unstaged consumer changes through real preparation and publication.
func TestNativePublicationIntegrationPreparedArtifact(t *testing.T) {
	f := newNativeIntegrationFixture(t)
	second := f.addSource(2, "internal/go.mod", strings.ReplaceAll(f.git("show", f.base+":internal/go.mod")+"\n", "v1.0.0", "v1.1.0"))
	sources := []nativePublicationSource{{Number: 1, HeadSHA: f.pulls[1]["head"].(map[string]any)["sha"].(string)}, {Number: 2, HeadSHA: second}}
	group, err := f.publisher.sourceGroup(context.Background(), sources)
	if err != nil {
		t.Fatal(err)
	}
	if err := nativeValidateTreeMetadata(group); err != nil {
		t.Fatalf("group violates native tree contract: %v", err)
	}
	group["blockers"], group["next_action"] = []any{}, map[string]any{"action": "prepare_replacement"}
	artifacts := t.TempDir()
	inventory, err := json.Marshal(map[string]any{"repository": "owner/repo", "base_sha": f.base, "groups": []any{group}})
	if err != nil {
		t.Fatal(err)
	}
	options := NativePrepareOptions{Repository: "owner/repo", Root: f.root, Inventory: filepath.Join(artifacts, "inventory.json"), Group: strings.TrimPrefix(f.branch, "native-dependabot-"), Target: "v1.1.0", TitleFile: filepath.Join(artifacts, "title"), BodyFile: filepath.Join(artifacts, "body"), Artifacts: filepath.Join(artifacts, "agent")}
	nativeRequestWrite(t, options.Inventory, inventory)
	nativeRequestWrite(t, options.TitleFile, []byte("chore(deps): upgrade example library"))
	nativeRequestWrite(t, options.BodyFile, []byte("Committed, staged and unstaged consumers; café."))
	f.write("backend/go.mod", strings.ReplaceAll(f.git("show", f.base+":backend/go.mod")+"\n", "v1.0.0", "v1.1.0"))
	f.commit("candidate committed consumer")
	f.writeGo("v1.1.0", "2")
	generated := "vendor/example.com/library/new.go"
	f.write(generated, "package library\n// generated consumer\n")
	f.git("add", generated)
	indexBefore, err := os.ReadFile(filepath.Join(f.root, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	f.boundary = func(route string) { t.Errorf("offline preparation accessed GitHub: %s", route) }
	first, err := PrepareNativeRequest(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(options.Artifacts, nativeRequestFile)
	payload, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	again, err := PrepareNativeRequest(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	repeatedPayload, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	indexAfter, err := os.ReadFile(filepath.Join(f.root, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, again) || !bytes.Equal(payload, repeatedPayload) || !bytes.Equal(indexBefore, indexAfter) {
		t.Fatal("preparation altered the candidate index or produced nondeterministic artifact bytes")
	}
	output, err := json.Marshal(map[string]any{"items": []any{map[string]any{"type": "native_replacement", "request_sha256": first["request_sha256"]}}})
	if err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(artifacts, "agent_output.json")
	nativeRequestWrite(t, outputPath, output)
	request, err := ReadNativePublicationRequest(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if request == nil || request.request.ExpectedBaseSHA != f.base || !reflect.DeepEqual(request.request.SourcePRs, sources) {
		t.Fatalf("request lost immutable selection: %v", request)
	}
	for _, filename := range []string{"backend/go.mod", "internal/go.mod", generated} {
		if !strings.Contains(request.request.Patch, "diff --git a/"+filename) {
			t.Fatalf("prepared artifact lost consumer %s", filename)
		}
	}
	if !strings.HasSuffix(request.request.Patch, "\n") {
		t.Fatal("preparation truncated patch newline")
	}
	f.boundary = nil
	result := f.published(request)
	head := result["head_sha"].(string)
	for _, filename := range []string{"backend/go.mod", "internal/go.mod", "image-sync/oc-mirror/acrauth/go.mod"} {
		if !strings.Contains(f.remoteGit("show", head+":"+filename), "v1.1.0") {
			t.Fatalf("prepared consumer not published: %s", filename)
		}
	}
	if !strings.Contains(f.remoteGit("show", head+":"+generated), "generated consumer") {
		t.Fatal("staged generated addition lost")
	}
	if f.pulls[100]["title"] != request.request.Title || !strings.HasPrefix(f.pulls[100]["body"].(string), request.request.Body+"\n\n") {
		t.Fatal("prepared metadata did not reach ready publication")
	}
}
