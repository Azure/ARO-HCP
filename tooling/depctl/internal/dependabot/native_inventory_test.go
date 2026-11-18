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
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const nativeTestGroup = "0123456789abcdef01234567"

// NativeFixture constructs a complete eligible inventory without external evidence.
func nativeFixture() map[string]any {
	return map[string]any{
		"repository": "Azure/ARO-HCP", "base_sha": strings.Repeat("a", 40),
		"groups": []any{map[string]any{
			"group": nativeTestGroup, "target": "v1.2.3", "ecosystem": "go", "family": "example.com/module",
			"next_action": map[string]any{"action": "prepare_replacement"}, "blockers": []any{},
			"sources": []any{map[string]any{
				"number": 7, "head_sha": strings.Repeat("b", 40), "group": nativeTestGroup,
				"target": "v1.2.3", "ecosystem": "go", "family": "example.com/module",
				"intents": []any{map[string]any{
					"ecosystem": "go", "package": "example.com/module", "manifest": "tooling/go.mod",
					"lock_manifests": []any{"go.mod", "tooling/go.mod"},
				}},
			}},
		}},
	}
}

// NativeGroup extracts the fixture's selected entry.
func nativeGroup(inventory map[string]any) map[string]any {
	return inventory["groups"].([]any)[0].(map[string]any)
}

// NativeSource extracts the fixture's first source.
func nativeSource(inventory map[string]any) map[string]any {
	return nativeGroup(inventory)["sources"].([]any)[0].(map[string]any)
}

// NativeIntent extracts the fixture's first source intent.
func nativeIntent(inventory map[string]any) map[string]any {
	return nativeSource(inventory)["intents"].([]any)[0].(map[string]any)
}

// NativePayload serializes test data without silently ignoring errors.
func nativePayload(t *testing.T, inventory map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func TestSelectNativeInventoryGroupPreservesExactCandidate(t *testing.T) {
	for _, action := range []string{"prepare_replacement", "maintain_replacement"} {
		t.Run(action, func(t *testing.T) {
			inventory := nativeFixture()
			group := nativeGroup(inventory)
			group["next_action"] = map[string]any{"action": action}
			group["future_metadata"] = json.RawMessage(`{"exact":9007199254740993,"values":[null,true,"keep"]}`)
			inventory["groups"] = append([]any{map[string]any{
				"group": nativeTestGroup, "target": "v0.9.0", "next_action": map[string]any{"action": "close_superseded"},
			}}, inventory["groups"].([]any)...)
			selection, err := SelectNativeInventoryGroup(strings.NewReader(nativePayload(t, inventory)), "Azure/ARO-HCP", nativeTestGroup, "v1.2.3")
			if err != nil {
				t.Fatal(err)
			}
			if selection.BaseSHA != strings.Repeat("a", 40) || strings.Join(selection.Manifests, ",") != "go.mod,tooling/go.mod" {
				t.Fatalf("selection = %+v", selection)
			}
			if !strings.Contains(string(selection.Group), `"exact":9007199254740993`) || !strings.Contains(string(selection.Group), `"target":"v1.2.3"`) {
				t.Fatalf("selected metadata was altered: %s", selection.Group)
			}
		})
	}
}

func TestSelectNativeInventoryGroupRejectsInvalidInventory(t *testing.T) {
	cases := map[string]func(map[string]any){
		"foreign repository":    func(v map[string]any) { v["repository"] = "Other/Repo" },
		"null inventory groups": func(v map[string]any) { v["groups"] = nil },
		"null group entry":      func(v map[string]any) { v["groups"] = []any{nil} },
		"nonobject group":       func(v map[string]any) { v["groups"] = []any{[]any{}} },
		"invalid base":          func(v map[string]any) { v["base_sha"] = "main" },
		"null base":             func(v map[string]any) { v["base_sha"] = nil },
		"boolean base":          func(v map[string]any) { v["base_sha"] = true },
		"duplicate exact pair":  func(v map[string]any) { v["groups"] = []any{nativeGroup(v), nativeGroup(v)} },
		"unknown target":        func(v map[string]any) { nativeGroup(v)["target"] = "unknown" },
		"mixed source target":   func(v map[string]any) { nativeSource(v)["target"] = "v0.9.0" },
		"mixed source group":    func(v map[string]any) { nativeSource(v)["group"] = strings.Repeat("f", 24) },
		"mixed ecosystem":       func(v map[string]any) { nativeSource(v)["ecosystem"] = "npm" },
		"mixed family":          func(v map[string]any) { nativeSource(v)["family"] = "other" },
		"unknown ecosystem":     func(v map[string]any) { nativeGroup(v)["ecosystem"] = "cargo" },
		"empty family":          func(v map[string]any) { nativeGroup(v)["family"] = "" },
		"null blockers":         func(v map[string]any) { nativeGroup(v)["blockers"] = nil },
		"blockers present":      func(v map[string]any) { nativeGroup(v)["blockers"] = []any{"blocked"} },
		"blockers object":       func(v map[string]any) { nativeGroup(v)["blockers"] = map[string]any{} },
		"missing next action":   func(v map[string]any) { delete(nativeGroup(v), "next_action") },
		"empty sources":         func(v map[string]any) { nativeGroup(v)["sources"] = []any{} },
		"null source":           func(v map[string]any) { nativeGroup(v)["sources"] = []any{nil} },
		"boolean number":        func(v map[string]any) { nativeSource(v)["number"] = true },
		"null number":           func(v map[string]any) { nativeSource(v)["number"] = nil },
		"string number":         func(v map[string]any) { nativeSource(v)["number"] = "7" },
		"fraction number":       func(v map[string]any) { nativeSource(v)["number"] = 7.5 },
		"exponent number":       func(v map[string]any) { nativeSource(v)["number"] = json.RawMessage(`7e0`) },
		"zero number":           func(v map[string]any) { nativeSource(v)["number"] = 0 },
		"negative number":       func(v map[string]any) { nativeSource(v)["number"] = -7 },
		"short head":            func(v map[string]any) { nativeSource(v)["head_sha"] = "abc" },
		"uppercase head":        func(v map[string]any) { nativeSource(v)["head_sha"] = strings.Repeat("B", 40) },
		"boolean head":          func(v map[string]any) { nativeSource(v)["head_sha"] = true },
		"null head":             func(v map[string]any) { nativeSource(v)["head_sha"] = nil },
		"duplicate PR":          func(v map[string]any) { nativeGroup(v)["sources"] = []any{nativeSource(v), nativeSource(v)} },
		"unsorted PRs": func(v map[string]any) {
			other := nativeSource(nativeFixture())
			other["number"] = 6
			nativeGroup(v)["sources"] = []any{nativeSource(v), other}
		},
		"too many sources": func(v map[string]any) {
			sources := make([]any, 101)
			for i := range sources {
				source := nativeSource(nativeFixture())
				source["number"] = i + 1
				sources[i] = source
			}
			nativeGroup(v)["sources"] = sources
		},
		"empty intents":          func(v map[string]any) { nativeSource(v)["intents"] = []any{} },
		"null intent":            func(v map[string]any) { nativeSource(v)["intents"] = []any{nil} },
		"intent ecosystem":       func(v map[string]any) { nativeIntent(v)["ecosystem"] = "npm" },
		"intent empty package":   func(v map[string]any) { nativeIntent(v)["package"] = "" },
		"intent boolean package": func(v map[string]any) { nativeIntent(v)["package"] = true },
		"intent wrong family":    func(v map[string]any) { nativeIntent(v)["package"] = "example.com/other" },
		"null manifest":          func(v map[string]any) { nativeIntent(v)["manifest"] = nil },
		"null locks":             func(v map[string]any) { nativeIntent(v)["lock_manifests"] = nil },
		"object locks":           func(v map[string]any) { nativeIntent(v)["lock_manifests"] = map[string]any{} },
		"null lock":              func(v map[string]any) { nativeIntent(v)["lock_manifests"] = []any{nil} },
	}
	for _, action := range []string{"blocked", "close_superseded", "wait_for_closure", "admit"} {
		cases["ineligible "+action] = func(v map[string]any) { nativeGroup(v)["next_action"] = map[string]any{"action": action} }
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			inventory := nativeFixture()
			mutate(inventory)
			if _, err := SelectNativeInventoryGroup(strings.NewReader(nativePayload(t, inventory)), "Azure/ARO-HCP", nativeTestGroup, "v1.2.3"); err == nil {
				t.Fatal("invalid inventory accepted")
			}
		})
	}
}

func TestSelectNativeInventoryGroupRejectsUnsafePaths(t *testing.T) {
	for _, path := range []string{"", "/go.mod", "../go.mod", "a/../go.mod", "./go.mod", "a//go.mod", "a/", ".git/config", "a/.git/config", ".GIT/config", `a\go.mod`, "a b/go.mod", "a\t/go.mod", "a\x00/go.mod", "a\u00a0/go.mod", "C:/go.mod"} {
		for _, field := range []string{"manifest", "lock_manifests"} {
			t.Run(fmt.Sprintf("%s/%q", field, path), func(t *testing.T) {
				inventory := nativeFixture()
				if field == "manifest" {
					nativeIntent(inventory)[field] = path
				} else {
					nativeIntent(inventory)[field] = []any{path}
				}
				if _, err := SelectNativeInventoryGroup(strings.NewReader(nativePayload(t, inventory)), "Azure/ARO-HCP", nativeTestGroup, "v1.2.3"); err == nil {
					t.Fatal("unsafe path accepted")
				}
			})
		}
	}
}

func TestSelectNativeInventoryGroupRejectsMalformedJSON(t *testing.T) {
	valid := nativePayload(t, nativeFixture())
	cases := map[string]string{
		"null": "null", "array": "[]", "truncated": valid[:len(valid)-1],
		"trailing object": valid + "{}", "trailing garbage": valid + "x",
		"duplicate root":    strings.Replace(valid, `"repository":`, `"repository":"Other/Repo","repository":`, 1),
		"duplicate nested":  strings.Replace(valid, `"manifest":`, `"manifest":"evil","manifest":`, 1),
		"escaped duplicate": strings.Replace(valid, `"manifest":`, `"mani\u0066est":"evil","manifest":`, 1),
		"case alias":        strings.Replace(valid, `"manifest":`, `"Manifest":`, 1),
		"invalid UTF8":      strings.Replace(valid, "tooling/go.mod", "tooling/\xffgo.mod", 1),
		"oversize":          valid + strings.Repeat(" ", MaxNativeInventoryBytes),
		"deep nesting":      strings.Repeat("[", 258) + "0" + strings.Repeat("]", 258),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := SelectNativeInventoryGroup(strings.NewReader(input), "Azure/ARO-HCP", nativeTestGroup, "v1.2.3"); err == nil {
				t.Fatal("malformed JSON accepted")
			}
		})
	}
}

func TestSelectNativeInventoryGroupValidatesArguments(t *testing.T) {
	for _, args := range [][3]string{
		{"../Azure/ARO-HCP", nativeTestGroup, "v1.2.3"}, {"Azure/ARO-HCP", "bad", "v1.2.3"},
		{"Azure/ARO-HCP", strings.Repeat("A", 24), "v1.2.3"}, {"Azure/ARO-HCP", nativeTestGroup, ""},
	} {
		if _, err := SelectNativeInventoryGroup(strings.NewReader(nativePayload(t, nativeFixture())), args[0], args[1], args[2]); err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
}

func TestNativeInventoryFamilySemantics(t *testing.T) {
	for _, test := range []struct{ ecosystem, pkg, family string }{
		{"go", "github.com/Azure/azure-sdk-for-go/sdk/azcore", "github.com/Azure/azure-sdk-for-go"},
		{"go", "github.com/Azure/azure-sdk-for-go", "github.com/Azure/azure-sdk-for-go"},
		{"go", "example.com/module/v2", "example.com/module/v2"},
		{"npm", "@typespec/compiler", "typespec"},
		{"npm", "@azure-tools/typespec-client-generator-core", "typespec"},
		{"npm", "@other/package", "@other/package"},
		{"actions", "azure/login/path", "azure/login"},
	} {
		t.Run(test.pkg, func(t *testing.T) {
			inventory := nativeFixture()
			group, source, intent := nativeGroup(inventory), nativeSource(inventory), nativeIntent(inventory)
			group["ecosystem"], source["ecosystem"], intent["ecosystem"] = test.ecosystem, test.ecosystem, test.ecosystem
			group["family"], source["family"], intent["package"] = test.family, test.family, test.pkg
			delete(intent, "lock_manifests")
			if _, err := SelectNativeInventoryGroup(strings.NewReader(nativePayload(t, inventory)), "Azure/ARO-HCP", nativeTestGroup, "v1.2.3"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSelectNativeInventoryGroupAcceptsSourceBoundaryAndExactNumbers(t *testing.T) {
	inventory := nativeFixture()
	sources := make([]any, 100)
	for i := range sources {
		source := nativeSource(nativeFixture())
		source["number"] = i + 1
		sources[i] = source
	}
	sources[99].(map[string]any)["number"] = json.RawMessage(`90071992547409931234567890`)
	nativeGroup(inventory)["sources"] = sources
	if _, err := SelectNativeInventoryGroup(strings.NewReader(nativePayload(t, inventory)+"\n \t"), "Azure/ARO-HCP", nativeTestGroup, "v1.2.3"); err != nil {
		t.Fatal(err)
	}
}
