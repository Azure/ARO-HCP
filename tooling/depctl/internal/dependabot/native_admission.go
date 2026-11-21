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
	"context"
	"path"
	"strings"
)

// NativeAdmissionSafe requires complete native consumers before admission, without running them.
func NativeAdmissionSafe(ctx context.Context, read NativeReadFunc, group map[string]any, paths, available, live map[string]bool, reader func(string) (string, error)) error {
	if group["ecosystem"] == "go" {
		return nativeReject("native Go workspace/vendor completeness requires a validated replacement")
	}
	if group["family"] == "typespec" {
		return nativeReject("TypeSpec generated consumers require a validated replacement")
	}
	intents, err := nativeRowsValue(group["intents"])
	if err != nil {
		return err
	}
	metadata := map[string]any{"ecosystem": group["ecosystem"], "family": group["family"], "sources": []any{map[string]any{"intents": group["intents"]}}}
	if err := nativeValidateTreeMetadata(metadata); err != nil {
		return err
	}
	if group["ecosystem"] == "actions" {
		for _, item := range intents {
			if !live[item["manifest"].(string)] {
				return nativeReject("obsolete native workflow cannot be admitted")
			}
		}
		for filename := range paths {
			if strings.HasSuffix(filename, ".md") && !paths[strings.TrimSuffix(filename, ".md")+".lock.yml"] {
				return nativeReject("native workflow source lacks regenerated compiler lock")
			}
			if strings.HasSuffix(filename, ".lock.yml") {
				source := strings.TrimSuffix(filename, ".lock.yml") + ".md"
				if live[source] && !available[source] {
					return nativeReject("native compiled workflow lost its existing source")
				}
			}
		}
		return NativeVerifyCoverage(ctx, read, intents, reader, available)
	}
	for _, item := range intents {
		filename := item["manifest"].(string)
		if path.Base(filename) != "package.json" {
			continue
		}
		lock := path.Join(path.Dir(filename), "package-lock.json")
		if !paths[lock] {
			return nativeReject("native npm manifest lacks updated lock consumer")
		}
		text, err := reader(lock)
		if err != nil {
			return err
		}
		resolved, err := nativeReadLock(text)
		if err != nil {
			return err
		}
		resolution, ok := resolved["node_modules/"+item["package"].(string)]
		if !ok || !nativeNPMCovers(resolution.Version, item["target"], true) {
			return nativeReject("native npm lock does not resolve intended target")
		}
	}
	return nil
}
