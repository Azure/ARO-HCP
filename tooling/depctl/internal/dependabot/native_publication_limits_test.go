// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package dependabot

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	automation "github.com/Azure/ARO-HCP/tooling/github"
)

// TestNativePublicationFileLimit exercises strict artifact reading and the public publisher at the complete-inventory boundary.
func TestNativePublicationFileLimit(t *testing.T) {
	for _, test := range []struct {
		name  string
		files int
	}{
		{name: "2999 accepted", files: automation.MaxPublicationFiles - 1},
		{name: "3000 rejected", files: automation.MaxPublicationFiles},
		{name: "3001 rejected", files: automation.MaxPublicationFiles + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newNativeIntegrationFixture(t)
			request := f.request("v1.1.0", "2", 1)
			paths, err := nativePatchPaths(request.request.Patch)
			if err != nil {
				t.Fatal(err)
			}
			var patch strings.Builder
			patch.WriteString(request.request.Patch)
			last := ""
			for index := len(paths); index < test.files; index++ {
				last = fmt.Sprintf("vendor/example.com/library/generated_%04d.go", index)
				fmt.Fprintf(&patch, "diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1 @@\n+package library\n", last, last, last)
			}
			request.request.Patch = patch.String()
			artifactRoot := t.TempDir()
			if err := os.Mkdir(filepath.Join(artifactRoot, "agent"), 0700); err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(request.request)
			if err != nil {
				t.Fatal(err)
			}
			nativeRequestWrite(t, filepath.Join(artifactRoot, "agent", nativeRequestFile), payload)
			outputPath := filepath.Join(artifactRoot, "output.json")
			nativeRequestWrite(t, outputPath, []byte(fmt.Sprintf(`{"items":[{"type":"native_replacement","request_sha256":"%x"}]}`, sha256.Sum256(payload))))
			remoteBefore := f.remoteGit("show-ref")
			localBefore := f.git("show-ref")
			branchBefore := f.git("symbolic-ref", "HEAD")
			rejected := test.files >= automation.MaxPublicationFiles
			if rejected {
				f.boundary = func(route string) { t.Errorf("rejected request accessed GitHub: %s", route) }
			}
			validated, readErr := ReadNativePublicationRequest(outputPath)
			if !rejected {
				if readErr != nil || validated == nil {
					t.Fatalf("strict request read failed: %v", readErr)
				}
				f.published(validated)
				if got := f.remoteGit("show", "refs/heads/"+f.branch+":"+last); got != "package library" {
					t.Fatalf("published final boundary file = %q", got)
				}
				return
			}
			if readErr == nil || !strings.Contains(readErr.Error(), "fewer than 3000 files") || validated != nil {
				t.Fatalf("strict request read = %v, %v; want file-limit rejection", validated, readErr)
			}
			if _, err := f.publish(request); err == nil || !strings.Contains(err.Error(), "fewer than 3000 files") {
				t.Fatalf("Publish error = %v; want file-limit rejection", err)
			}
			if len(f.writes) != 0 || len(f.pulls) != 1 {
				t.Fatalf("rejected request mutated GitHub: writes=%v pulls=%v", f.writes, f.pulls)
			}
			if got := f.remoteGit("show-ref"); got != remoteBefore {
				t.Fatalf("rejected request changed remote refs: before=%s after=%s", remoteBefore, got)
			}
			if got := f.git("show-ref"); got != localBefore {
				t.Fatalf("rejected request changed local refs: before=%s after=%s", localBefore, got)
			}
			if got := f.git("symbolic-ref", "HEAD"); got != branchBefore {
				t.Fatalf("rejected request changed current branch: before=%s after=%s", branchBefore, got)
			}
		})
	}
}
