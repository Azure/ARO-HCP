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

// verify-cosmos-ids walks JSON files and checks that Cosmos DB document "id"
// fields match arm.ResourceIDStringToCosmosID(resourceID) for the associated
// ARM resource ID on the same document.
//
// A file is checked when it has a top-level "id" and a discoverable ARM
// resource ID (top-level resourceID/resourceId, properties.resourceID/resourceId,
// or properties.cosmosMetadata.resourceID). Wrong UUIDs and legacy pipe-style
// ids are both reported as violations with the corrected id.
//
// Exit code is 1 if any mismatches are found.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

var uuidRE = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type violation struct {
	path         string
	resourcePath string
	resourceID   string
	gotID        string
	wantID       string
	reason       string
}

func main() {
	verbose := flag.Bool("verbose", false, "print skipped files and summary counts")
	tsv := flag.Bool("tsv", false, "print violations as TSV lines: path\\tgot\\twant\\treason (for scripting)")
	flag.Parse()

	dirs := flag.Args()
	if len(dirs) == 0 {
		dirs = []string{"."}
	}

	var violations []violation
	checked := 0
	skipped := 0

	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".json") {
				return nil
			}

			v, ok, skipReason, err := checkFile(path)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			if !ok {
				skipped++
				if *verbose && !*tsv && skipReason != "" {
					fmt.Printf("skip %s: %s\n", path, skipReason)
				}
				return nil
			}

			checked++
			if v != nil {
				violations = append(violations, *v)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error walking %s: %v\n", dir, err)
			os.Exit(2)
		}
	}

	if len(violations) > 0 {
		if *tsv {
			for _, v := range violations {
				// TSV: path, got id, correct id, reason
				fmt.Printf("%s\t%s\t%s\t%s\n",
					escapeTSV(v.path),
					escapeTSV(v.gotID),
					escapeTSV(v.wantID),
					escapeTSV(v.reason),
				)
			}
			os.Exit(1)
		}

		fmt.Println("ERROR: Cosmos document id does not match arm.ResourceIDStringToCosmosID(resourceID):")
		fmt.Println()
		for _, v := range violations {
			fmt.Printf("%s\n", v.path)
			fmt.Printf("  got id:      %s\n", v.gotID)
			fmt.Printf("  correct id:  %s\n", v.wantID)
			if v.reason != "" {
				fmt.Printf("  reason:      %s\n", v.reason)
			}
			if *verbose {
				fmt.Printf("  %s = %s\n", v.resourcePath, v.resourceID)
			}
			fmt.Println()
		}
		fmt.Printf("Found %d mismatch(es) in %d checked file(s).\n", len(violations), checked)
		os.Exit(1)
	}

	if *tsv {
		os.Exit(0)
	}
	if *verbose {
		fmt.Printf("OK: %d Cosmos document fixture(s) checked, %d JSON file(s) skipped.\n", checked, skipped)
	} else {
		fmt.Printf("OK: %d Cosmos document fixture(s) checked.\n", checked)
	}
}

func escapeTSV(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\t", "\\t")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}

func checkFile(path string) (*violation, bool, string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, false, "", err
	}

	var doc map[string]any
	if err := json.Unmarshal(content, &doc); err != nil {
		return nil, false, "not a JSON object", nil
	}
	if doc == nil {
		return nil, false, "empty JSON object", nil
	}

	id, ok := doc["id"].(string)
	if !ok || id == "" {
		return nil, false, "no top-level id", nil
	}

	resourceID, resourcePath := extractResourceID(doc)
	if resourceID == "" {
		return nil, false, "no ARM resource ID on document", nil
	}

	wantID, err := arm.ResourceIDStringToCosmosID(resourceID)
	if err != nil {
		return nil, false, fmt.Sprintf("invalid resource ID %q: %v", resourceID, err), nil
	}

	if id == wantID {
		return nil, true, "", nil
	}

	reason := "id does not match ResourceIDStringToCosmosID(resourceID)"
	switch {
	case strings.Contains(id, "|"):
		reason = "legacy pipe-style cosmos id"
	case !uuidRE.MatchString(id):
		reason = "top-level id is not a UUID"
	}

	return &violation{
		path:         path,
		resourcePath: resourcePath,
		resourceID:   resourceID,
		gotID:        id,
		wantID:       wantID,
		reason:       reason,
	}, true, "", nil
}

func extractResourceID(doc map[string]any) (string, string) {
	if v, ok := stringField(doc, "resourceID"); ok && isARMResourceID(v) {
		return v, "resourceID"
	}
	if v, ok := stringField(doc, "resourceId"); ok && isARMResourceID(v) {
		return v, "resourceId"
	}

	props, ok := doc["properties"].(map[string]any)
	if !ok {
		return "", ""
	}

	if v, ok := stringField(props, "resourceID"); ok && isARMResourceID(v) {
		return v, "properties.resourceID"
	}
	if v, ok := stringField(props, "resourceId"); ok && isARMResourceID(v) {
		return v, "properties.resourceId"
	}

	if cm, ok := props["cosmosMetadata"].(map[string]any); ok {
		if v, ok := stringField(cm, "resourceID"); ok && isARMResourceID(v) {
			return v, "properties.cosmosMetadata.resourceID"
		}
	}

	return "", ""
}

func stringField(obj map[string]any, key string) (string, bool) {
	v, ok := obj[key].(string)
	return v, ok && v != ""
}

func isARMResourceID(s string) bool {
	return strings.HasPrefix(strings.ToLower(s), "/subscriptions/")
}
