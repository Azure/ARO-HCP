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

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const pipelineFixture = `orgs:
- org: Azure
  repos:
    - name: ARO-HCP
      branches:
        - main
      mode:
        trigger: %s
    - name: another-repository
      mode:
        trigger: manual
- org: another-org
  repos:
    - name: another-project
      mode:
        trigger: auto
`

const retesterFixture = `retester:
  max_retests_for_sha: 3
  orgs:
    Azure:
      enabled: true
      repos:
        ARO-HCP:
          enabled: %s
        another-repository:
          enabled: true
    another-org:
      enabled: true
`

func writeConfigs(t *testing.T, root string, pipeline, retester []byte) {
	t.Helper()
	for path, content := range map[string][]byte{pipelinePath: pipeline, retesterPath: retester} {
		file := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readConfig(t *testing.T, root, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func TestRoundTripPreservesUnrelatedSettings(t *testing.T) {
	root := filepath.Join(t.TempDir(), "release checkout")
	pipeline := []byte(fmt.Sprintf(pipelineFixture, "auto"))
	retester := []byte(fmt.Sprintf(retesterFixture, "true"))
	writeConfigs(t, root, pipeline, retester)
	if err := applyToggle(root, "enable"); err != nil {
		t.Fatal(err)
	}
	if got := readConfig(t, root, pipelinePath); string(got) != fmt.Sprintf(pipelineFixture, "manual") {
		t.Fatalf("unexpected enabled pipeline config:\n%s", got)
	}
	if got := readConfig(t, root, retesterPath); string(got) != fmt.Sprintf(retesterFixture, "false") {
		t.Fatalf("unexpected disabled retester config:\n%s", got)
	}
	if err := applyToggle(root, "disable"); err != nil {
		t.Fatal(err)
	}
	assertConfigs(t, root, pipeline, retester)
}

func assertConfigs(t *testing.T, root string, pipeline, retester []byte) {
	t.Helper()
	if !bytes.Equal(readConfig(t, root, pipelinePath), pipeline) || !bytes.Equal(readConfig(t, root, retesterPath), retester) {
		t.Fatal("configuration bytes differ from the expected snapshot")
	}
}

func TestRejectedTransitionsDoNotWrite(t *testing.T) {
	for _, tc := range []struct{ action, mode, retries string }{
		{"enable", "manual", "false"}, {"disable", "auto", "true"},
		{"enable", "manual", "true"}, {"enable", "auto", "false"},
		{"disable", "manual", "true"}, {"disable", "auto", "false"},
		{"unknown", "auto", "true"},
	} {
		t.Run(tc.action+"-"+tc.mode+"-"+tc.retries, func(t *testing.T) {
			pipeline := []byte(fmt.Sprintf(pipelineFixture, tc.mode))
			retester := []byte(fmt.Sprintf(retesterFixture, tc.retries))
			root := t.TempDir()
			writeConfigs(t, root, pipeline, retester)
			if err := applyToggle(root, tc.action); err == nil {
				t.Fatal("invalid transition accepted")
			}
			assertConfigs(t, root, pipeline, retester)
		})
	}
}

func TestAmbiguousMissingAndReformattedTargetsRejected(t *testing.T) {
	for _, action := range []string{"enable", "disable"} {
		mode, retries := "auto", "true"
		if action == "disable" {
			mode, retries = "manual", "false"
		}
		for _, mutation := range []struct {
			name, path, old, replacement string
		}{
			{"duplicate pipeline repo", pipelinePath, "    - name: another-repository\n", "    - name: \"ARO-HCP\"\n"},
			{"duplicate pipeline org", pipelinePath, "- org: another-org\n", "- org: \"Azure\"\n"},
			{"missing pipeline repo", pipelinePath, "name: ARO-HCP", "name: missing"},
			{"missing pipeline org", pipelinePath, "org: Azure", "org: missing"},
			{"duplicate mode", pipelinePath, "        trigger: " + mode + "\n", "        trigger: auto\n        trigger: manual\n"},
			{"duplicate retester repo", retesterPath, "        another-repository:\n", "        \"ARO-HCP\":\n"},
			{"duplicate retester org", retesterPath, "    another-org:\n", "    \"Azure\":\n"},
			{"missing retester repo", retesterPath, "        ARO-HCP:\n", "        missing:\n"},
			{"missing retester org", retesterPath, "    Azure:\n", "    missing:\n"},
			{"duplicate retester state", retesterPath, "          enabled: " + retries + "\n", "          enabled: true\n          enabled: false\n"},
			{"pipeline formatting drift", pipelinePath, "org: Azure", "org: \"Azure\""},
			{"retester formatting drift", retesterPath, "        ARO-HCP:\n", "        \"ARO-HCP\":\n"},
			{"additional branch", pipelinePath, "        - main\n", "        - main\n        - release\n"},
			{"shorthand repo", pipelinePath, "    - name: another-repository\n      mode:\n        trigger: manual\n", "    - ARO-HCP\n"},
			{"extra pipeline document", pipelinePath, "orgs:\n", "ignored: true\n---\norgs:\n"},
			{"extra retester document", retesterPath, "retester:\n", "ignored: true\n---\nretester:\n"},
		} {
			t.Run(action+"/"+mutation.name, func(t *testing.T) {
				pipeline := fmt.Sprintf(pipelineFixture, mode)
				retester := fmt.Sprintf(retesterFixture, retries)
				if mutation.path == pipelinePath {
					pipeline = strings.Replace(pipeline, mutation.old, mutation.replacement, 1)
				} else {
					retester = strings.Replace(retester, mutation.old, mutation.replacement, 1)
				}
				root := t.TempDir()
				writeConfigs(t, root, []byte(pipeline), []byte(retester))
				if err := applyToggle(root, action); err == nil {
					t.Fatal("invalid configuration accepted")
				}
				assertConfigs(t, root, []byte(pipeline), []byte(retester))
			})
		}
	}
}

func TestReleaseConfigsRoundTrip(t *testing.T) {
	repository := os.Getenv("RELEASE_REPO")
	if repository == "" {
		t.Skip("set RELEASE_REPO to test copies of a real release checkout")
	}
	pipeline := readConfig(t, repository, pipelinePath)
	retester := readConfig(t, repository, retesterPath)
	root := t.TempDir()
	writeConfigs(t, root, pipeline, retester)
	expectedPipeline, _, err := decodeConfig(pipeline)
	if err != nil {
		t.Fatal(err)
	}
	expectedRetester, _, err := decodeConfig(retester)
	if err != nil {
		t.Fatal(err)
	}
	first, second := "enable", "disable"
	var mode map[string]any
	for _, org := range expectedPipeline["orgs"].([]any) {
		org := org.(map[string]any)
		if org["org"] != "Azure" {
			continue
		}
		for _, repo := range org["repos"].([]any) {
			if repo, ok := repo.(map[string]any); ok && repo["name"] == "ARO-HCP" {
				mode = repo["mode"].(map[string]any)
			}
		}
	}
	if mode == nil {
		t.Fatal("missing ARO-HCP main pipeline")
	}
	switch mode["trigger"] {
	case "manual":
		first, second = second, first
		mode["trigger"] = "auto"
	case "auto":
		mode["trigger"] = "manual"
	default:
		t.Fatal("unexpected ARO-HCP pipeline mode")
	}
	target, err := objectAt(expectedRetester, "retester", "orgs", "Azure", "repos", "ARO-HCP")
	if err != nil {
		t.Fatal(err)
	}
	target["enabled"] = first == "disable"
	if err := applyToggle(root, first); err != nil {
		t.Fatal(err)
	}
	actualPipeline, _, err := decodeConfig(readConfig(t, root, pipelinePath))
	if err != nil {
		t.Fatal(err)
	}
	actualRetester, _, err := decodeConfig(readConfig(t, root, retesterPath))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actualPipeline, expectedPipeline) || !reflect.DeepEqual(actualRetester, expectedRetester) {
		t.Fatal("toggle changed unexpected settings")
	}
	if err := applyToggle(root, second); err != nil {
		t.Fatal(err)
	}
	assertConfigs(t, root, pipeline, retester)
}

func TestReformattedTargetWithUnrelatedAnchorRejected(t *testing.T) {
	for _, action := range []string{"enable", "disable"} {
		mode, retries := "auto", "true"
		if action == "disable" {
			mode, retries = "manual", "false"
		}
		for _, path := range []string{pipelinePath, retesterPath} {
			t.Run(action+"/"+path, func(t *testing.T) {
				pipeline := fmt.Sprintf(pipelineFixture, mode)
				retester := fmt.Sprintf(retesterFixture, retries)
				if path == pipelinePath {
					decoy := strings.Replace(pipeline, "orgs:", "unrelated:", 1)
					pipeline = strings.Replace(pipeline, "org: Azure", "org: \"Azure\"", 1) + decoy
				} else {
					decoy := strings.Replace(retester, "retester:", "unrelated:", 1)
					retester = strings.Replace(retester, "ARO-HCP:", "\"ARO-HCP\":", 1) + decoy
				}
				root := t.TempDir()
				writeConfigs(t, root, []byte(pipeline), []byte(retester))
				if err := applyToggle(root, action); err == nil {
					t.Fatal("reformatted target accepted using an unrelated anchor")
				}
				assertConfigs(t, root, []byte(pipeline), []byte(retester))
			})
		}
	}
}

func TestCanonicalTargetsLeaveUnrelatedAnchorsUntouched(t *testing.T) {
	pipeline := fmt.Sprintf(pipelineFixture, "auto")
	retester := fmt.Sprintf(retesterFixture, "true")
	pipelineDecoy := strings.Replace(pipeline, "orgs:", "unrelated:", 1)
	retesterDecoy := strings.Replace(retester, "retester:", "unrelated:", 1)
	root := t.TempDir()
	writeConfigs(t, root, []byte(pipeline+pipelineDecoy), []byte(retester+retesterDecoy))
	if err := applyToggle(root, "enable"); err != nil {
		t.Fatal(err)
	}
	assertConfigs(t, root, []byte(fmt.Sprintf(pipelineFixture, "manual")+pipelineDecoy),
		[]byte(fmt.Sprintf(retesterFixture, "false")+retesterDecoy))
}
