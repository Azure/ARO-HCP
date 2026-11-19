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

// NativeValidateTreeMetadata rejects partial policy inputs before Git or API access.
func nativeValidateTreeMetadata(group map[string]any) error {
	ecosystem, _ := group["ecosystem"].(string)
	family, _ := group["family"].(string)
	if family == "" || ecosystem != "go" && ecosystem != "npm" && ecosystem != "actions" {
		return nativeReject("missing or unsupported upgrade family")
	}
	sources, ok := group["sources"].([]any)
	if !ok || len(sources) == 0 {
		return nativeReject("missing source evidence")
	}
	for _, raw := range sources {
		source, err := nativeObject(raw)
		if err != nil {
			return err
		}
		intents, err := nativeRowsValue(source["intents"])
		if err != nil {
			return err
		}
		if len(intents) == 0 {
			return nativeReject("missing source intent")
		}
		for _, intent := range intents {
			if err := nativeValidateCoverageIntent(intent); err != nil {
				return err
			}
			if intent["ecosystem"] != ecosystem {
				return nativeReject("source intent does not match selected ecosystem")
			}
		}
	}
	return nil
}

// NativeValidateCoverageIntent rejects unsupported identities and ambiguous coverage paths.
func nativeValidateCoverageIntent(intent map[string]any) error {
	ecosystem, _ := intent["ecosystem"].(string)
	pkg, _ := intent["package"].(string)
	manifest, _ := intent["manifest"].(string)
	target, targetOK := intent["target"].(string)
	if pkg == "" || !safeNativePath(manifest) || nativeEcosystem(manifest) != ecosystem {
		return nativeReject("invalid coverage identity")
	}
	_, targetPresent := intent["target"]
	removedGo := ecosystem == "go" && targetPresent && intent["target"] == nil
	if !removedGo && (!targetOK || target == "") {
		return nativeReject("missing coverage target")
	}
	switch ecosystem {
	case "go":
		if !removedGo && !nativeVersionCovers(target, target) {
			return nativeReject("invalid Go coverage target")
		}
	case "npm":
		if _, _, ok := nativeNPMVersion(target); !ok {
			return nativeReject("invalid npm coverage target")
		}
	case "actions":
		if !nativeSHA.MatchString(target) {
			return nativeReject("invalid Actions coverage target")
		}
	default:
		return nativeReject("unsupported coverage ecosystem")
	}
	if ecosystem != "actions" && intent["key"] == nil {
		return nativeReject("missing dependency occurrence key")
	}
	if raw, exists := intent["lock_manifests"]; exists {
		var locks []string
		switch value := raw.(type) {
		case []string:
			locks = value
		case []any:
			for _, item := range value {
				name, ok := item.(string)
				if !ok {
					return nativeReject("invalid lock coverage path")
				}
				locks = append(locks, name)
			}
		default:
			return nativeReject("invalid lock coverage metadata")
		}
		for _, name := range locks {
			if !safeNativePath(name) || !nativeLockPath(name) {
				return nativeReject("invalid lock coverage path")
			}
		}
	}
	return nil
}
