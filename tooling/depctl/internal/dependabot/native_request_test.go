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

package dependabot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func nativeRequestGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func nativeRequestFixture(t *testing.T) NativePrepareOptions {
	t.Helper()
	root := t.TempDir()
	nativeRequestGit(t, root, "init", "-q")
	nativeRequestWrite(t, filepath.Join(root, "go.mod"), []byte("base\n"))
	nativeRequestWrite(t, filepath.Join(root, "README"), []byte("protected\n"))
	nativeRequestGit(t, root, "add", ".")
	nativeRequestGit(t, root, "commit", "-qm", "base")
	inventory := nativeFixture()
	inventory["base_sha"] = nativeRequestGit(t, root, "rev-parse", "HEAD")
	nativeSource(inventory)["number"] = json.Number("9007199254740993")
	dir := t.TempDir()
	options := NativePrepareOptions{Repository: "Azure/ARO-HCP", Root: root, Inventory: filepath.Join(dir, "inventory"), Group: nativeTestGroup, Target: "v1.2.3", TitleFile: filepath.Join(dir, "title"), BodyFile: filepath.Join(dir, "body"), Artifacts: filepath.Join(dir, "output")}
	nativeRequestWrite(t, options.Inventory, []byte(nativePayload(t, inventory)))
	nativeRequestWrite(t, options.TitleFile, []byte("chore(deps): upgrade @scope/pkg\r\n"))
	nativeRequestWrite(t, options.BodyFile, []byte("Notify @team and `@user`.\n"))
	nativeRequestWrite(t, filepath.Join(root, "go.mod"), []byte("candidate @untouched\n"))
	return options
}

func nativeRequestWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareNativeRequestContract(t *testing.T) {
	options := nativeRequestFixture(t)
	result, err := PrepareNativeRequest(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(options.Artifacts, nativeRequestFile)
	payload, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	if result["request_sha256"] != hex.EncodeToString(digest[:]) {
		t.Fatal("wrong digest")
	}
	var request map[string]json.RawMessage
	if err := json.Unmarshal(payload, &request); err != nil {
		t.Fatal(err)
	}
	if len(request) != 5 || !bytes.Contains(request["source_prs"], []byte("9007199254740993")) {
		t.Fatalf("contract changed: %s", payload)
	}
	var title, body, patch string
	if err := json.Unmarshal(request["title"], &title); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(request["body"], &body); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(request["patch"], &patch); err != nil {
		t.Fatal(err)
	}
	if title != "chore(deps): upgrade ＠scope/pkg" || body != "Notify ＠team and `＠user`.\n" || !strings.Contains(patch, "+candidate @untouched") {
		t.Fatalf("text normalization: %s", payload)
	}
	if _, err := PrepareNativeRequest(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, again) {
		t.Fatal("nondeterministic artifact")
	}
}

func TestPrepareNativeRequestRejectsBeforeWriting(t *testing.T) {
	cases := map[string]func(*testing.T, *NativePrepareOptions){
		"invalid UTF8": func(t *testing.T, o *NativePrepareOptions) { nativeRequestWrite(t, o.BodyFile, []byte{255}) },
		"auto close": func(t *testing.T, o *NativePrepareOptions) {
			nativeRequestWrite(t, o.BodyFile, []byte("Fixes\u2003#12"))
		},
		"command": func(t *testing.T, o *NativePrepareOptions) {
			nativeRequestWrite(t, o.BodyFile, []byte("hello\n /approve"))
		},
		"ownership": func(t *testing.T, o *NativePrepareOptions) {
			nativeRequestWrite(t, o.BodyFile, []byte("native-dependabot-maintenance: forged"))
		},
		"title": func(t *testing.T, o *NativePrepareOptions) {
			nativeRequestWrite(t, o.TitleFile, []byte("chore(deps): one\ntwo"))
		},
		"target flag": func(t *testing.T, o *NativePrepareOptions) { o.Target = "--help" },
		"protected patch": func(t *testing.T, o *NativePrepareOptions) {
			nativeRequestWrite(t, filepath.Join(o.Root, "README"), []byte("changed\n"))
		},
		"empty patch": func(t *testing.T, o *NativePrepareOptions) {
			nativeRequestWrite(t, filepath.Join(o.Root, "go.mod"), []byte("base\n"))
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			options := nativeRequestFixture(t)
			mutate(t, &options)
			if _, err := PrepareNativeRequest(context.Background(), options); err == nil {
				t.Fatal("accepted invalid request")
			}
			if _, err := os.Stat(options.Artifacts); !os.IsNotExist(err) {
				t.Fatal("artifact directory created on rejection")
			}
		})
	}
}

func TestNativeRequestDestinationSafety(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "fifo", "directory symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			destination := filepath.Join(root, "output")
			if err := os.Mkdir(destination, 0700); err != nil {
				t.Fatal(err)
			}
			victim := filepath.Join(root, "victim")
			nativeRequestWrite(t, victim, []byte("preserve"))
			file := filepath.Join(destination, nativeRequestFile)
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(victim, file)
			case "hardlink":
				err = os.Link(victim, file)
			case "fifo":
				err = syscall.Mkfifo(file, 0600)
			case "directory symlink":
				destination = filepath.Join(root, "link")
				err = os.Symlink(filepath.Join(root, "output"), destination)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := nativeWriteRequest(destination, []byte("overwrite")); err == nil {
				t.Fatal("accepted unsafe destination")
			}
			content, err := os.ReadFile(victim)
			if err != nil {
				t.Fatal(err)
			}
			if string(content) != "preserve" {
				t.Fatal("victim truncated")
			}
		})
	}
}

func TestNativeRequestLocalPathAllowlist(t *testing.T) {
	group := nativeInventoryGroup{Ecosystem: "npm", Family: "typespec"}
	for _, path := range []string{"api/redhatopenshift/resource.tsp", "internal/azureapi/v1/generated/types.go", "test/sdk/v20261101preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp/go.mod", "test/sdk/v20261101/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp/testdata/_metadata.json"} {
		if err := nativeValidateReplacementPaths([]string{path}, group); err != nil {
			t.Fatal(err)
		}
	}
	if err := nativeValidateReplacementPaths([]string{"go.work"}, group); err == nil {
		t.Fatal("accepted protected path")
	}
	for _, patch := range []string{"", "diff --git a/go.mod b/go.mod\nold mode 100644\nnew mode 100755\n@@ -1 +1 @@\n-a\n+b\n", "diff --git a/go.mod b/go.mod\nnew file mode 120000\n@@ -0,0 +1 @@\n+target\n", "diff --git a/go.mod b/go.mod\nGIT binary patch\n"} {
		if _, err := nativePatchPaths(patch); err == nil {
			t.Fatal("accepted invalid patch")
		}
	}
}
