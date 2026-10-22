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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// QueryExecutor executes concrete KQL and returns the result columns and rows.
// Library templates reference fully qualified cluster().database() tables, so
// the executor does not need to select a database.
type QueryExecutor func(ctx context.Context, kql string) (columns []string, rows [][]string, err error)

// LibrarySessionConfig configures a LibrarySession.
type LibrarySessionConfig struct {
	// Config holds the values of the config-sourced parameters (ClusterURI and
	// the database names).
	Config map[string]string
	// Execute runs rendered queries.
	Execute QueryExecutor
	// ResultsDir is where each run writes its snapshot-style markdown output.
	ResultsDir string
	// LibraryDir is where the library files were written for the investigator
	// to read; used to print absolute paths of READMEs and guides. Optional.
	LibraryDir string
}

// LibrarySession renders and runs library queries for one investigation,
// remembering parameter values supplied by the investigator and provided by
// query results so that later queries can reuse them.
type LibrarySession struct {
	lib *QueryLibrary
	cfg LibrarySessionConfig

	mu     sync.Mutex
	values map[string]sessionValue
	runs   int
}

// sessionValue is a remembered parameter value.
type sessionValue struct {
	value  string
	source string
	// deps holds the parameter values the producing query ran with. A provided
	// value is stale once any of them resolves differently.
	deps map[string]string
}

// NewLibrarySession creates a session over lib.
func NewLibrarySession(lib *QueryLibrary, cfg LibrarySessionConfig) *LibrarySession {
	return &LibrarySession{lib: lib, cfg: cfg, values: map[string]sessionValue{}}
}

// Library returns the session's query library.
func (s *LibrarySession) Library() *QueryLibrary {
	return s.lib
}

// resolution is the outcome of resolving one parameter.
type resolution struct {
	value  string
	source string
	// stale describes a remembered value that was ignored because the values
	// it was produced from have changed.
	stale string
}

// resolve finds the value of a parameter: config value, then call argument,
// then derivation, then remembered value, then default. It must be called
// with s.mu held.
func (s *LibrarySession) resolve(name string, args map[string]string, visiting map[string]bool) resolution {
	if visiting[name] {
		return resolution{}
	}
	visiting[name] = true
	defer delete(visiting, name)

	def := s.lib.params[name]
	if def != nil && def.Source == ParamSourceConfig {
		if v := s.cfg.Config[name]; v != "" {
			return resolution{value: v, source: "config"}
		}
		return resolution{}
	}
	if v := args[name]; v != "" {
		return resolution{value: v, source: "argument"}
	}
	if def != nil {
		for _, d := range def.Derive {
			from := s.resolve(d.From, args, visiting)
			if from.value == "" {
				continue
			}
			if v := deriveFuncs[d.Func](from.value); v != "" {
				return resolution{value: v, source: "derived from " + d.From}
			}
		}
	}
	var stale string
	if sv, ok := s.values[name]; ok {
		if changed := s.changedDeps(sv, args, visiting); len(changed) == 0 {
			return resolution{value: sv.value, source: sv.source}
		} else {
			stale = fmt.Sprintf("ignored %q (%s) because %s changed since", sv.value, sv.source, strings.Join(changed, ", "))
		}
	}
	if def != nil && def.Default != "" {
		if r := s.resolve(def.Default, args, visiting); r.value != "" {
			return resolution{value: r.value, source: "defaults to " + def.Default, stale: stale}
		}
	}
	return resolution{stale: stale}
}

// changedDeps returns the dependencies of a remembered value that now resolve
// differently.
func (s *LibrarySession) changedDeps(sv sessionValue, args map[string]string, visiting map[string]bool) []string {
	var changed []string
	for dep, want := range sv.deps {
		if got := s.resolve(dep, args, visiting).value; !strings.EqualFold(got, want) {
			changed = append(changed, dep)
		}
	}
	sort.Strings(changed)
	return changed
}

// normalizeArgs validates call arguments against a query's parameters and
// normalizes datetime values to RFC3339 UTC.
func (s *LibrarySession) normalizeArgs(q *QueryDef, args map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for name, v := range args {
		v = strings.TrimSpace(v)
		def := s.lib.params[name]
		if def == nil && q.param(name) == nil {
			return nil, fmt.Errorf("unknown parameter %q for %s; it accepts %s (or any parameter from describe_param)",
				name, q.Name, strings.Join(s.agentParams(q), ", "))
		}
		if def != nil && def.Source == ParamSourceConfig {
			return nil, fmt.Errorf("parameter %s is fixed by the CLI configuration and cannot be set", name)
		}
		if v == "" {
			continue
		}
		switch s.lib.paramType(q, name) {
		case ParamTypeDatetime:
			t, err := parseLibraryTime(v)
			if err != nil {
				return nil, fmt.Errorf("parameter %s: %w", name, err)
			}
			v = t.UTC().Format(time.RFC3339)
		case ParamTypeInt:
			if _, err := strconv.Atoi(v); err != nil {
				return nil, fmt.Errorf("parameter %s: %q is not an integer", name, v)
			}
		}
		out[name] = v
	}
	return out, nil
}

// agentParams returns the names of a query's non-config parameters.
func (s *LibrarySession) agentParams(q *QueryDef) []string {
	var names []string
	for _, p := range q.Params {
		if def := s.lib.params[p.Name]; def != nil && def.Source == ParamSourceConfig {
			continue
		}
		names = append(names, p.Name)
	}
	return names
}

// parseLibraryTime parses the time formats accepted for datetime parameters.
func parseLibraryTime(v string) (time.Time, error) {
	v = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(v), "datetime("), ")")
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not a datetime; use RFC3339, for example 2025-01-02T15:04:05Z", v)
}

// ParamStatus describes how one parameter of a query currently resolves.
type ParamStatus struct {
	Name     string
	Hint     string
	Optional bool
	Config   bool
	Value    string
	Source   string
	Stale    string
	// Providers lists the queries whose results provide the parameter.
	Providers []string
	// DeriveFrom lists the parameters it can be derived from.
	DeriveFrom []string
	Default    string
	Guide      string
}

// paramStatuses resolves every parameter of a query. It must be called with
// s.mu held.
func (s *LibrarySession) paramStatuses(q *QueryDef, args map[string]string) []ParamStatus {
	out := make([]ParamStatus, 0, len(q.Params))
	for _, p := range q.Params {
		r := s.resolve(p.Name, args, map[string]bool{})
		st := ParamStatus{
			Name:      p.Name,
			Hint:      s.lib.paramHint(q, p.Name),
			Optional:  s.lib.paramOptional(q, p.Name),
			Value:     r.value,
			Source:    r.source,
			Stale:     r.stale,
			Providers: s.lib.providers(p.Name),
		}
		if def := s.lib.params[p.Name]; def != nil {
			st.Config = def.Source == ParamSourceConfig
			st.Default = def.Default
			st.Guide = def.Guide
			for _, d := range def.Derive {
				st.DeriveFrom = append(st.DeriveFrom, d.From)
			}
		}
		out = append(out, st)
	}
	return out
}

// missingError explains which parameters are missing and how to obtain them.
func (s *LibrarySession) missingError(q *QueryDef, statuses []ParamStatus) error {
	byName := map[string]ParamStatus{}
	var missing []ParamStatus
	for _, st := range statuses {
		byName[st.Name] = st
		if st.Value == "" && !st.Optional {
			missing = append(missing, st)
		}
	}
	var unsatisfied bool
	if len(q.RequireAnyOf) > 0 {
		unsatisfied = true
		for _, g := range q.RequireAnyOf {
			ok := true
			for _, n := range g {
				if byName[n].Value == "" {
					ok = false
				}
			}
			if ok {
				unsatisfied = false
			}
		}
	}
	if len(missing) == 0 && !unsatisfied {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "cannot run %s: missing parameters.\n", q.Name)
	for _, st := range missing {
		b.WriteString(s.formatMissing(st))
	}
	if unsatisfied {
		b.WriteString("At least one of these groups must be fully known:\n")
		for _, g := range q.RequireAnyOf {
			fmt.Fprintf(&b, "  - %s\n", strings.Join(g, " + "))
		}
		seen := map[string]bool{}
		for _, g := range q.RequireAnyOf {
			for _, n := range g {
				if st := byName[n]; st.Value == "" && !seen[n] {
					seen[n] = true
					b.WriteString(s.formatMissing(st))
				}
			}
		}
	}
	return fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
}

// formatMissing describes how to obtain one missing parameter.
func (s *LibrarySession) formatMissing(st ParamStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- %s: %s", st.Name, st.Hint)
	if len(st.Providers) > 0 {
		fmt.Fprintf(&b, " Provided by running %s.", strings.Join(st.Providers, " or "))
	}
	if st.Stale != "" {
		fmt.Fprintf(&b, " (Remembered value %s.)", st.Stale)
	}
	b.WriteString("\n")
	return b.String()
}

// templateData builds the template data map from resolved statuses.
func (s *LibrarySession) templateData(q *QueryDef, statuses []ParamStatus) (map[string]any, error) {
	data := map[string]any{}
	for _, st := range statuses {
		switch s.lib.paramType(q, st.Name) {
		case ParamTypeDatetime:
			if st.Value == "" {
				data[st.Name] = time.Time{}
				continue
			}
			t, err := parseLibraryTime(st.Value)
			if err != nil {
				return nil, fmt.Errorf("parameter %s: %w", st.Name, err)
			}
			data[st.Name] = t
		case ParamTypeInt:
			n, _ := strconv.Atoi(st.Value)
			data[st.Name] = n
		default:
			data[st.Name] = st.Value
		}
	}
	return data, nil
}

// RunResult is the outcome of running a library query.
type RunResult struct {
	Query    string
	Rendered string
	// File is the path of the snapshot-style markdown output.
	File    string
	Columns []string
	Rows    [][]string
	Params  []ParamStatus
	// Provided holds parameter values this run provided and remembered.
	Provided map[string]string
	// Ambiguous holds parameters this run returned several values for; none
	// was remembered.
	Ambiguous map[string][]string
	// Warnings holds problems extracting provided values.
	Warnings []string
}

// Render resolves a query's parameters and renders it without executing it.
func (s *LibrarySession) Render(name string, args map[string]string) (string, []ParamStatus, error) {
	q := s.lib.queries[name]
	if q == nil {
		return "", nil, s.unknownQuery(name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	args, err := s.normalizeArgs(q, args)
	if err != nil {
		return "", nil, err
	}
	statuses := s.paramStatuses(q, args)
	if err := s.missingError(q, statuses); err != nil {
		return "", statuses, err
	}
	data, err := s.templateData(q, statuses)
	if err != nil {
		return "", statuses, err
	}
	rendered, err := renderQuery(q.templatePath, data)
	return rendered, statuses, err
}

// Run resolves a query's parameters, renders and executes it, writes its
// output as snapshot-style markdown, and remembers the parameter values its
// results provide. Explicit arguments are remembered for later queries.
func (s *LibrarySession) Run(ctx context.Context, name string, args map[string]string) (*RunResult, error) {
	q := s.lib.queries[name]
	if q == nil {
		return nil, s.unknownQuery(name)
	}

	s.mu.Lock()
	args, err := s.normalizeArgs(q, args)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	statuses := s.paramStatuses(q, args)
	if err := s.missingError(q, statuses); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	data, err := s.templateData(q, statuses)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	for n, v := range args {
		s.values[n] = sessionValue{value: v, source: "set by an earlier run_query argument"}
	}
	s.runs++
	runNumber := s.runs
	deps := map[string]string{}
	for _, st := range statuses {
		if _, provided := q.Provides[st.Name]; provided || st.Config || st.Value == "" {
			continue
		}
		if s.lib.paramType(q, st.Name) == ParamTypeDatetime {
			continue
		}
		deps[st.Name] = st.Value
	}
	s.mu.Unlock()

	rendered, err := renderQuery(q.templatePath, data)
	if err != nil {
		return nil, err
	}
	columns, rows, err := s.cfg.Execute(ctx, rendered)
	if err != nil {
		return nil, fmt.Errorf("query %s failed: %w\n\nRendered KQL:\n%s", q.Name, err, rendered)
	}

	result := &RunResult{
		Query:     q.Name,
		Rendered:  rendered,
		Columns:   columns,
		Rows:      rows,
		Params:    statuses,
		Provided:  map[string]string{},
		Ambiguous: map[string][]string{},
	}

	if s.cfg.ResultsDir != "" {
		resultRows := make([]resultRow, 0, len(rows))
		for _, r := range rows {
			resultRows = append(resultRows, resultRow{columns: columns, values: r})
		}
		file := filepath.Join(s.cfg.ResultsDir, fmt.Sprintf("%03d-%s.md", runNumber, strings.ReplaceAll(q.Name, "/", "-")))
		if err := os.MkdirAll(s.cfg.ResultsDir, 0o755); err != nil {
			return nil, fmt.Errorf("failed to create results directory: %w", err)
		}
		if err := os.WriteFile(file, []byte(composeOutput(q.readme(), rendered, resultRows)), 0o644); err != nil {
			return nil, fmt.Errorf("failed to write query result: %w", err)
		}
		result.File = file
	}

	provided := sortedKeys(q.Provides)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, param := range provided {
		values, err := q.Provides[param].values(columns, rows)
		switch {
		case err != nil:
			result.Warnings = append(result.Warnings, fmt.Sprintf("could not extract %s: %v", param, err))
		case len(values) == 1:
			s.values[param] = sessionValue{value: values[0], source: "provided by " + q.Name, deps: deps}
			result.Provided[param] = values[0]
		case len(values) > 1:
			result.Ambiguous[param] = values
		}
	}
	return result, nil
}

func (s *LibrarySession) unknownQuery(name string) error {
	return fmt.Errorf("unknown query %q; use list_queries to find query names (format <component>/<queryName>)", name)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
