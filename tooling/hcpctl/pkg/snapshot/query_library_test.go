// Copyright 2025 Microsoft Corporation
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

package snapshot

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// TestWriteQueryLibraryCopiesEveryEmbeddedFile ensures the exported library
// is a byte-for-byte copy of every embedded README, KQL template, and partial.
func TestWriteQueryLibraryCopiesEveryEmbeddedFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteQueryLibrary(dir); err != nil {
		t.Fatalf("WriteQueryLibrary: %v", err)
	}

	var readmes, templates int
	err := fs.WalkDir(queriesFS, queriesRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		switch {
		case d.Name() == "README.md":
			readmes++
		case strings.HasSuffix(d.Name(), ".kql"):
			templates++
		}
		want, err := queriesFS.ReadFile(p)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(p, queriesRoot+"/"))))
		if err != nil {
			t.Errorf("library missing %s: %v", p, err)
			return nil
		}
		if string(got) != string(want) {
			t.Errorf("library copy of %s differs from the embedded file", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if readmes == 0 || templates == 0 {
		t.Fatalf("expected embedded READMEs and templates, got %d READMEs and %d templates", readmes, templates)
	}
	if _, err := os.Stat(filepath.Join(dir, QueryLibraryIndexFile)); err != nil {
		t.Errorf("library missing %s: %v", QueryLibraryIndexFile, err)
	}
}

// TestQueryLibraryIndexListsEveryQuery ensures the index lists every query
// directory, including ones not registered in allQueries.
func TestQueryLibraryIndexListsEveryQuery(t *testing.T) {
	index, err := QueryLibraryIndex()
	if err != nil {
		t.Fatalf("QueryLibraryIndex: %v", err)
	}
	err = fs.WalkDir(queriesFS, queriesRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "query.kql" {
			return err
		}
		dir := path.Dir(strings.TrimPrefix(p, queriesRoot+"/"))
		if !strings.Contains(index, "| `"+dir+"/` |") {
			t.Errorf("index does not list %s", dir)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"`hypershift/hostedClusterConditionTimeline/`",
		"`conditions/hypershift/hostedClusterConditionTimeline.md`",
		"`.HCPDatabase`",
		"`partials/bundleMap.kql`",
	} {
		if !strings.Contains(index, want) {
			t.Errorf("index missing %q", want)
		}
	}
}

// TestQueryTemplateVariablesDocumented ensures every queryData field is
// documented and every variable referenced by a template is a known field.
func TestQueryTemplateVariablesDocumented(t *testing.T) {
	documented := make(map[string]bool, len(queryTemplateVariables))
	for _, v := range queryTemplateVariables {
		if v.description == "" {
			t.Errorf("template variable %s has no description", v.name)
		}
		documented[v.name] = true
	}
	fields := make(map[string]bool)
	typ := reflect.TypeOf(queryData{})
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		fields[name] = true
		if !documented[name] {
			t.Errorf("queryData field %s is not documented in queryTemplateVariables", name)
		}
	}
	for name := range documented {
		if !fields[name] {
			t.Errorf("queryTemplateVariables documents unknown field %s", name)
		}
	}

	varRef := regexp.MustCompile(`\.([A-Z][A-Za-z]*)`)
	action := regexp.MustCompile(`\{\{.*?\}\}`)
	err := fs.WalkDir(queriesFS, queriesRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".kql") {
			return err
		}
		data, err := queriesFS.ReadFile(p)
		if err != nil {
			return err
		}
		for _, a := range action.FindAllString(string(data), -1) {
			for _, m := range varRef.FindAllStringSubmatch(a, -1) {
				if !fields[m[1]] {
					t.Errorf("%s references unknown template variable .%s", p, m[1])
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
