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
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var nativeReleaseSHAVersion = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// NativeVerifyOfficialRelease proves an exact commit using official repository tags,
// whose commit SHAs are peeled for both lightweight and annotated tags.
func NativeVerifyOfficialRelease(ctx context.Context, read NativeReadFunc, repository, version, expectedSHA string) error {
	endpoint := "repos/" + repository + "/tags?per_page=100&page="
	if read == nil || !nativeReleaseEndpoint.MatchString(endpoint+"1") || version == "" || strings.ContainsAny(version, " \t\r\n\x00") || !nativeSHA.MatchString(expectedSHA) {
		return nativeReject("invalid official release identity")
	}
	shaVersion := nativeReleaseSHAVersion.MatchString(version)
	if shaVersion && version != expectedSHA {
		return nativeReject("raw release commit does not match intended pin")
	}
	seen := map[string]bool{}
	for page := 1; page <= 100; page++ {
		value, err := nativeRead(ctx, read, endpoint+strconv.Itoa(page), false, false)
		if err != nil {
			return fmt.Errorf("reading official tags for %s page %d: %w", repository, page, err)
		}
		tags, ok := value.([]any)
		if !ok || len(tags) > 100 {
			return nativeReject("missing or malformed official repository tags")
		}
		matched := false
		for _, raw := range tags {
			tag, err := nativeObject(raw)
			if err != nil {
				return err
			}
			name, nameOK := tag["name"].(string)
			commit, commitOK := tag["commit"].(map[string]any)
			sha, shaOK := commit["sha"].(string)
			if !nameOK || name == "" || strings.ContainsAny(name, " \t\r\n\x00") || !commitOK || !shaOK || !nativeSHA.MatchString(sha) || seen[name] {
				return nativeReject("malformed or duplicate official repository tag")
			}
			seen[name] = true
			if !shaVersion && name == version && sha != expectedSHA {
				return nativeReject("official release tag does not match intended pin")
			}
			matched = matched || (!shaVersion && name == version) || (shaVersion && sha == expectedSHA)
		}
		if matched {
			return nil
		}
		if len(tags) < 100 {
			return nativeReject("official repository tags do not prove intended release")
		}
	}
	return nativeReject("official release tag search exceeds 100 pages")
}
