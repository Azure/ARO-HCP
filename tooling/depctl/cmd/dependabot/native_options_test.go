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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/pkg/depctl"
)

func TestNativeRequestMissingInputsLeaveNoArtifacts(t *testing.T) {
	for _, command := range []string{"prepare-request", "validate-candidate"} {
		for _, missing := range []string{"--repository", "--inventory", "--group", "--target", "--title-file", "--body-file", "--artifact-dir"} {
			t.Run(command+"/"+missing, func(t *testing.T) {
				directory := t.TempDir()
				artifacts := filepath.Join(directory, "artifacts")
				flags := []string{"--repository", "Azure/ARO-HCP", "--inventory", filepath.Join(directory, "absent.json"), "--group", nativeCommandGroup, "--target", "v1.2.3", "--title-file", filepath.Join(directory, "title"), "--body-file", filepath.Join(directory, "body"), "--artifact-dir", artifacts}
				args := []string{"native", command}
				for i := 0; i < len(flags); i += 2 {
					if flags[i] != missing {
						args = append(args, flags[i:i+2]...)
					}
				}
				output, err := executeNativeCommand(t, args)
				var exit *depctl.ExitError
				if !errors.As(err, &exit) || exit.Code != depctl.ExitInvalid || !strings.Contains(err.Error(), "are required") || output != "" {
					t.Fatalf("want required-input error before reading files, got stdout=%q error=%v", output, err)
				}
				if _, err := os.Stat(artifacts); !os.IsNotExist(err) {
					t.Fatalf("invalid input created artifacts: %v", err)
				}
			})
		}
	}
}

func TestNativePolicyRejectsMalformedEvidenceWithoutRemoteReads(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, "remote-read")
	t.Setenv("NATIVE_POLICY_READ_MARKER", marker)
	if err := os.WriteFile(filepath.Join(directory, "gh"), []byte("#!/bin/sh\nprintf x > \"$NATIVE_POLICY_READ_MARKER\"\nexit 97\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, name := range []string{"check-tree", "check-coverage", "check-admission"} {
		for _, payload := range []string{`{"unknown":true}`, `{} {}`, `{"intents":`} {
			t.Run(name+"/"+payload, func(t *testing.T) {
				command, err := NewCommand("")
				if err != nil {
					t.Fatal(err)
				}
				var output bytes.Buffer
				command.SetIn(strings.NewReader(payload))
				command.SetOut(&output)
				command.SetErr(&output)
				command.SilenceErrors, command.SilenceUsage = true, true
				command.SetArgs([]string{"native", name})
				err = command.Execute()
				var exit *depctl.ExitError
				if !errors.As(err, &exit) || exit.Code != depctl.ExitInvalid || output.Len() != 0 {
					t.Fatalf("malformed evidence must fail locally: output=%q error=%v", output.String(), err)
				}
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatalf("malformed policy evidence reached remote read: %v", err)
				}
			})
		}
	}
}

func TestNativeCoveragePathsOnlyEmitsOfflineJSON(t *testing.T) {
	command, err := NewCommand("")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command.SetIn(strings.NewReader(`{"intents":[]}`))
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"native", "check-coverage", "--paths-only"})
	// No executable can be used to satisfy this offline evidence request.
	t.Setenv("PATH", t.TempDir())
	if err := command.Execute(); err != nil || output.String() != "[]\n" {
		t.Fatalf("offline empty coverage must be a JSON array: output=%q error=%v", output.String(), err)
	}
}
