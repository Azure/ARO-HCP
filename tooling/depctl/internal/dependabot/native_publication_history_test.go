// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package dependabot

import (
	"strings"
	"testing"
)

// TestNativePublicationHistoryRechecks ignores repository activity but preserves predecessor authorization at write boundaries.
func TestNativePublicationHistoryRechecks(t *testing.T) {
	for _, boundary := range []string{"inventory before push", "inventory after push", "predecessor detail after push"} {
		for _, change := range []string{"repository pushed_at", "merge SHA", "ownership marker"} {
			t.Run(boundary+"/"+change, func(t *testing.T) {
				f := newNativeIntegrationFixture(t)
				first := f.published(f.request("v1.1.0", "2", 1))
				f.mergePublication(100)
				f.write("main-only.txt", "trusted intervening main change\n")
				f.commit("advance main")
				f.base = f.git("rev-parse", "HEAD")
				f.git("push", f.remote, "HEAD:refs/heads/main")
				sourceSHA := f.addSource(2, "backend/go.mod", strings.ReplaceAll(f.git("show", f.base+":backend/go.mod")+"\n", "v1.1.0", "v1.2.0"))
				request := f.request("v1.2.0", "3", 2)
				predecessor := f.pulls[100]
				baseRepository := predecessor["base"].(map[string]any)["repo"].(map[string]any)
				baseRepository["pushed_at"] = "2026-01-01T00:00:00Z"
				f.writes = nil
				ready, inventory, changed := false, false, false
				f.boundary = func(request string) {
					if request == "GET labels/"+nativePublicationLabel {
						ready = true
					}
					if request == "GET issues" {
						inventory = true
					}
					if request != "GET pulls/100" {
						return
					}
					fromInventory := inventory
					inventory = false
					if !ready || changed {
						return
					}
					if fromInventory != (boundary != "predecessor detail after push") {
						return
					}
					pushed := f.remoteGit("rev-parse", "refs/heads/"+f.branch) != first["head_sha"]
					if pushed != (boundary != "inventory before push") {
						return
					}
					changed = true
					switch change {
					case "repository pushed_at":
						baseRepository["pushed_at"] = "2026-01-01T00:00:01Z"
					case "merge SHA":
						predecessor["merge_commit_sha"] = f.base
					case "ownership marker":
						predecessor["body"] = strings.ReplaceAll(predecessor["body"].(string), f.pulls[1]["head"].(map[string]any)["sha"].(string), sourceSHA)
					}
				}
				result, err := f.publish(request)
				if !changed {
					t.Fatal("requested history boundary was not exercised")
				}
				if change == "repository pushed_at" {
					if err != nil {
						t.Fatalf("repository activity blocked successor publication: %v", err)
					}
					if result["number"] != 101 || result["reused"] != false {
						t.Fatalf("successor not published independently: %v", result)
					}
					if got := f.remoteGit("show", "-s", "--format=%P", result["head_sha"].(string)); got != first["head_sha"].(string)+" "+f.base {
						t.Fatalf("successor ancestry changed: %s", got)
					}
					return
				}
				wantError := "replacement history changed"
				if boundary == "predecessor detail after push" {
					wantError = "merged predecessor authorization changed"
				}
				if err == nil || !strings.Contains(err.Error(), wantError) {
					t.Fatalf("predecessor identity change was not rejected at %s: %v", boundary, err)
				}
				if len(f.writes) != 0 || f.pulls[101] != nil {
					t.Fatalf("history change allowed a PR write: %v", f.writes)
				}
				pushed := f.remoteGit("rev-parse", "refs/heads/"+f.branch) != first["head_sha"]
				if pushed != (boundary != "inventory before push") {
					t.Fatal("history rejection occurred on the wrong side of branch publication")
				}
			})
		}
	}
}
