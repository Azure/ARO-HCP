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
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

func TestJobFlags(t *testing.T) {
	for _, commandName := range []string{"run-suite", "run-test"} {
		for _, tc := range []struct {
			name             string
			args             []string
			wantName, wantID string
		}{
			{name: "environment defaults", wantName: "ci-job", wantID: "123"},
			{name: "explicit overrides", args: []string{"--job-name=local", "--job-id=456"}, wantName: "local", wantID: "456"},
			{name: "independent override", args: []string{"--job-id=456"}, wantName: "ci-job", wantID: "456"},
			{name: "explicit empty", args: []string{"--job-name=", "--job-id="}},
		} {
			t.Run(commandName+"/"+tc.name, func(t *testing.T) {
				t.Setenv("JOB_NAME", "ci-job")
				t.Setenv("BUILD_ID", "123")
				for _, key := range []string{framework.JobNameEnvvar, framework.JobIDEnvvar} {
					t.Setenv(key, "")
					if err := os.Unsetenv(key); err != nil {
						t.Fatal(err)
					}
				}
				root, err := setupCli()
				if err != nil {
					t.Fatal(err)
				}
				command, _, err := root.Find([]string{commandName})
				if err != nil {
					t.Fatal(err)
				}
				// Exercise the real pre-run hooks without launching Azure tests.
				command.Args = cobra.ArbitraryArgs
				command.RunE = func(*cobra.Command, []string) error {
					want := map[string]*string{}
					if tc.wantName != "" {
						want[framework.ProwJobNameTag] = &tc.wantName
					}
					if tc.wantID != "" {
						want[framework.ProwJobIDTag] = &tc.wantID
					}
					if got := framework.WithJobTags(map[string]*string{}); !reflect.DeepEqual(got, want) {
						t.Fatalf("parent tags = %v, want %v", got, want)
					}
					// Like the extension runner, spawn a child with no job flags.
					child := exec.Command(os.Args[0], "-test.run=^TestJobFlagsWorker$")
					child.Env = append(os.Environ(), "ARO_HCP_JOB_TAG_TEST_WORKER=true")
					output, err := child.Output()
					if err != nil {
						t.Fatalf("worker failed: %v", err)
					}
					var got map[string]*string
					if err := json.Unmarshal(output, &got); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("worker tags = %s, want %v", output, want)
					}
					return nil
				}
				args := []string{commandName}
				if commandName == "run-suite" {
					args = append(args, "integration/parallel")
				}
				root.SetArgs(append(args, tc.args...))
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				if os.Getenv("JOB_NAME") != "ci-job" || os.Getenv("BUILD_ID") != "123" {
					t.Fatal("runner changed the original CI environment")
				}
			})
		}
	}
}

func TestJobFlagsWorker(t *testing.T) {
	if os.Getenv("ARO_HCP_JOB_TAG_TEST_WORKER") != "true" {
		return
	}
	command := &cobra.Command{Use: "run-test"}
	hook := bindJobFlags(command)
	if err := hook(command, nil); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(framework.WithJobTags(map[string]*string{})); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

func TestJobFlagsValidation(t *testing.T) {
	for _, flag := range []string{"job-name", "job-id"} {
		for _, value := range []string{strings.Repeat("x", 257), strings.Repeat("😀", 129)} {
			t.Run(flag+"/"+value[:4], func(t *testing.T) {
				t.Setenv(framework.JobNameEnvvar, "original-name")
				t.Setenv(framework.JobIDEnvvar, "original-id")
				command := &cobra.Command{Use: "run-test"}
				hook := bindJobFlags(command)
				if err := command.ParseFlags([]string{"--" + flag + "=" + value}); err != nil {
					t.Fatal(err)
				}
				if err := hook(command, nil); err == nil || !strings.Contains(err.Error(), flag) {
					t.Fatalf("expected length error identifying %s, got %v", flag, err)
				}
				if framework.JobName() != "original-name" || framework.JobID() != "original-id" {
					t.Fatal("invalid flags changed the job environment")
				}
			})
		}
	}
}
