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

// Package kqlfunc parses Kusto function files ("{Database}_{FunctionName}.kql")
// and computes a valid dependency load order for them. This is the single
// place that understands Kusto function load-order constraints: a function
// referenced via "// @depends:" must be created before the function that
// depends on it. Every consumer that needs a correct load order (the kql
// emulator validator, the main.bicep generator, and the test manifest) should
// go through this package rather than maintaining its own ordering.
//
// "// @depends:" is a precise, authoritative, comma-separated list of the
// exact names of functions this one invokes (empty or omitted if none). It is
// not filtered, parsed as free text, or disambiguated by letter case: every
// name listed must be the exact name of a real function in the same
// database, or SortByDependency returns an error. Table names do not belong
// here: table-before-function load order is already guaranteed structurally
// (main.bicep deploys tables and functions as separate phases), so a table
// dependency carries no ordering information and should not be listed.
package kqlfunc

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// functionNameRe matches a function's declaration line, e.g.
// "EnrichWithConditionHistory(T: (...), p_X: string)", unindented, since
// that's the only such line in a well-formed function file.
var functionNameRe = regexp.MustCompile(`(?m)^([A-Z][A-Za-z0-9_]*)\(`)

// dependsLineRe matches the "// @depends: ..." header comment. The space
// before the capture group is "[ \t]*", not "\s*": "\s" includes newline, so
// "\s*" would consume past a blank depends line into the next line entirely.
var dependsLineRe = regexp.MustCompile(`(?m)^//\s*@depends:[ \t]*(.*)$`)

// File describes one parsed Kusto function file.
type File struct {
	// Path is the file's path, exactly as provided to ParseFiles.
	Path string
	// Database is the target database, derived from the filename's
	// "{Database}_" prefix (the part before the first underscore).
	Database string
	// Name is the function's declared name (without the database prefix).
	Name string
	// Depends lists the exact names of functions this one invokes, taken
	// verbatim (trimmed, comma-split) from "// @depends:". Empty if that
	// header is absent or blank.
	Depends []string
}

// FindFiles walks root and returns the path of every ".kql" file found.
func FindFiles(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".kql") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("finding .kql files under %q: %w", root, err)
	}
	return paths, nil
}

// ParseFiles reads and parses every given .kql function file.
func ParseFiles(paths []string) ([]File, error) {
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)

	files := make([]File, 0, len(sorted))
	for _, path := range sorted {
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %q: %w", path, err)
		}
		text := string(content)

		nameMatch := functionNameRe.FindStringSubmatch(text)
		if nameMatch == nil {
			return nil, fmt.Errorf("%q: could not find a function declaration (expected an unindented \"Name(\" line)", path)
		}

		var depends []string
		if dependsMatch := dependsLineRe.FindStringSubmatch(text); dependsMatch != nil {
			for _, name := range strings.Split(dependsMatch[1], ",") {
				name = strings.TrimSpace(name)
				if name != "" {
					depends = append(depends, name)
				}
			}
		}

		stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		database, _, ok := strings.Cut(stem, "_")
		if !ok {
			return nil, fmt.Errorf("%q: filename does not match the \"{Database}_{FunctionName}.kql\" convention", path)
		}

		files = append(files, File{
			Path:     path,
			Database: database,
			Name:     nameMatch[1],
			Depends:  depends,
		})
	}
	return files, nil
}

// SortByDependency groups files by database and topologically sorts each
// group by "// @depends:" references, so a file invoking another function is
// ordered after it. A function can only ever depend on another function in
// its own database (Kusto has no cross-database invoke), so every name in a
// file's Depends must resolve to a function in that file's own database
// group, or this returns an error. Files with no dependency keep their
// relative alphabetical order (ParseFiles already returns its input sorted).
// The returned map's slices are each a valid load order for that database.
func SortByDependency(files []File) (map[string][]File, error) {
	byDatabase := make(map[string][]File)
	for _, f := range files {
		byDatabase[f.Database] = append(byDatabase[f.Database], f)
	}

	result := make(map[string][]File, len(byDatabase))
	for database, group := range byDatabase {
		ordered, err := sortGroup(database, group)
		if err != nil {
			return nil, fmt.Errorf("database %q: %w", database, err)
		}
		result[database] = ordered
	}
	return result, nil
}

// sortGroup topologically sorts files belonging to a single database.
func sortGroup(database string, files []File) ([]File, error) {
	byName := make(map[string]*File, len(files))
	seenFiles := make(map[string]string, len(files))
	infos := make([]*File, len(files))
	for i := range files {
		infos[i] = &files[i]

		stem := strings.TrimSuffix(filepath.Base(files[i].Path), filepath.Ext(files[i].Path))
		if existing, ok := seenFiles[stem]; ok {
			return nil, fmt.Errorf("filename %q is declared in both %q and %q", stem, existing, files[i].Path)
		}
		seenFiles[stem] = files[i].Path

		if existing, ok := byName[files[i].Name]; ok {
			return nil, fmt.Errorf("function %q is declared in both %q and %q", files[i].Name, existing.Path, files[i].Path)
		}
		byName[files[i].Name] = infos[i]
	}

	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[string]int, len(infos))
	var order []File
	var visit func(info *File) error
	visit = func(info *File) error {
		switch state[info.Path] {
		case visited:
			return nil
		case visiting:
			return fmt.Errorf("circular function dependency detected involving %q", info.Path)
		}
		state[info.Path] = visiting
		for _, dep := range info.Depends {
			depInfo, ok := byName[dep]
			if !ok {
				return fmt.Errorf("%q declares \"@depends: %s\", but no function named %q exists in database %q (table names do not belong in @depends: table-before-function order is already guaranteed structurally)", info.Path, dep, dep, database)
			}
			if err := visit(depInfo); err != nil {
				return err
			}
		}
		state[info.Path] = visited
		order = append(order, *info)
		return nil
	}

	for _, info := range infos {
		if err := visit(info); err != nil {
			return nil, err
		}
	}

	return order, nil
}
