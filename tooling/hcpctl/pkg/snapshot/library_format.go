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
	"fmt"
	"path/filepath"
	"strings"
)

// ListMarkdown lists the library queries whose name, tags, or description
// contain filter (case-insensitive; empty matches all), with the parameters
// each needs and whether it can run with what the session knows now.
func (s *LibrarySession) ListMarkdown(filter string) string {
	filter = strings.ToLower(strings.TrimSpace(filter))
	s.mu.Lock()
	defer s.mu.Unlock()

	var b strings.Builder
	b.WriteString("| Query | Database | Description | Needs | Status |\n| --- | --- | --- | --- | --- |\n")
	matched := 0
	for _, name := range s.lib.names {
		q := s.lib.queries[name]
		haystack := strings.ToLower(name + " " + strings.Join(q.Tags, " ") + " " + q.Description)
		if filter != "" && !strings.Contains(haystack, filter) {
			continue
		}
		matched++
		statuses := s.paramStatuses(q, nil)
		var needs, missing []string
		for _, st := range statuses {
			if st.Config || st.Optional {
				continue
			}
			needs = append(needs, st.Name)
			if st.Value == "" {
				missing = append(missing, st.Name)
			}
		}
		if len(q.RequireAnyOf) > 0 {
			var groups []string
			groupOK := false
			for _, g := range q.RequireAnyOf {
				groups = append(groups, "("+strings.Join(g, " + ")+")")
				ok := true
				for _, n := range g {
					if s.resolve(n, nil, map[string]bool{}).value == "" {
						ok = false
					}
				}
				groupOK = groupOK || ok
			}
			needs = append(needs, strings.Join(groups, " or "))
			if !groupOK {
				missing = append(missing, "one of the groups")
			}
		}
		status := "ready"
		if len(missing) > 0 {
			status = "missing " + strings.Join(missing, ", ")
		}
		desc := q.Description
		if len(q.Provides) > 0 {
			desc += " Provides " + strings.Join(sortedKeys(q.Provides), ", ") + "."
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", name, q.Database, escapeMarkdownCell(desc), strings.Join(needs, ", "), status)
	}
	if matched == 0 {
		return fmt.Sprintf("No query matches %q. Call list_queries without a filter to see every query.", filter)
	}
	b.WriteString("\nNeeds omits optional parameters and the Kusto cluster/database, which are configured. ")
	b.WriteString("Parameters derived from others (for example SubscriptionID from ResourceID) count as known once their source is known. ")
	b.WriteString("Use describe_query for a query's README, parameter details and a rendered preview.\n")
	return b.String()
}

// DescribeQueryMarkdown documents one query: summary, parameters with their
// current values or how to obtain them, provided parameters, a rendered
// preview when possible, and the README.
func (s *LibrarySession) DescribeQueryMarkdown(name string) (string, error) {
	q := s.lib.queries[name]
	if q == nil {
		return "", s.unknownQuery(name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n%s\n\n", q.Name, q.Description)
	fmt.Fprintf(&b, "- Database: %s (`%s`)\n", q.Database, s.cfg.Config[libraryDatabases[q.Database]])
	if len(q.Tags) > 0 {
		fmt.Fprintf(&b, "- Tags: %s\n", strings.Join(q.Tags, ", "))
	}
	if q.Notes != "" {
		fmt.Fprintf(&b, "- Notes: %s\n", q.Notes)
	}

	statuses := s.paramStatuses(q, nil)
	b.WriteString("\n## Parameters\n\n| Parameter | Required | Current value | How to obtain |\n| --- | --- | --- | --- |\n")
	for _, st := range statuses {
		required := "yes"
		switch {
		case st.Config:
			required = "config"
		case q.inAnyOfGroup(st.Name):
			required = "group"
		case st.Optional:
			required = "optional"
		}
		value := "unknown"
		if st.Value != "" {
			value = fmt.Sprintf("`%s` (%s)", st.Value, st.Source)
		}
		if st.Stale != "" {
			value += "; " + st.Stale
		}
		how := st.Hint
		if len(st.Providers) > 0 {
			how += " Provided by " + strings.Join(st.Providers, ", ") + "."
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", st.Name, required, escapeMarkdownCell(value), escapeMarkdownCell(how))
	}
	if len(q.RequireAnyOf) > 0 {
		b.WriteString("\nAt least one of these groups must be fully known:\n\n")
		for _, g := range q.RequireAnyOf {
			fmt.Fprintf(&b, "- %s\n", strings.Join(g, " + "))
		}
	}

	if len(q.Provides) > 0 {
		b.WriteString("\n## Provides\n\nWhen the result holds exactly one distinct value, run_query remembers it for later queries:\n\n")
		for _, param := range sortedKeys(q.Provides) {
			p := q.Provides[param]
			from := "column `" + p.Column + "`"
			if p.Template != "" {
				from = "`" + p.Template + "` over the result columns"
			}
			fmt.Fprintf(&b, "- %s, from %s; used by %s\n", param, from, strings.Join(s.lib.consumers(param), ", "))
		}
	}

	if err := s.missingError(q, statuses); err != nil {
		fmt.Fprintf(&b, "\n## Preview\n\nNot renderable yet — %v\n", err)
	} else if data, err := s.templateData(q, statuses); err != nil {
		fmt.Fprintf(&b, "\n## Preview\n\nNot renderable: %v\n", err)
	} else if rendered, err := renderQuery(q.templatePath, data); err != nil {
		fmt.Fprintf(&b, "\n## Preview\n\nNot renderable: %v\n", err)
	} else {
		fmt.Fprintf(&b, "\n## Preview\n\nRendered with the current values:\n\n```kql\n%s\n```\n", strings.TrimRight(rendered, "\n"))
	}

	if readme := q.readme(); readme != "" {
		b.WriteString("\n## README\n\n")
		b.WriteString(readme)
	}
	return b.String(), nil
}

// DescribeParamMarkdown documents one parameter: its hint, current value,
// how it is derived or defaulted, which queries provide and use it, and its
// guide.
func (s *LibrarySession) DescribeParamMarkdown(name string) (string, error) {
	def := s.lib.params[name]
	if def == nil {
		var declaring []string
		for _, qn := range s.lib.names {
			if p := s.lib.queries[qn].param(name); p != nil {
				declaring = append(declaring, fmt.Sprintf("%s (%s)", qn, p.Hint))
			}
		}
		if len(declaring) > 0 {
			return fmt.Sprintf("# Parameter %s\n\nLocal parameter declared by %s.\n", name, strings.Join(declaring, "; ")), nil
		}
		return "", fmt.Errorf("unknown parameter %q; known parameters: %s", name, strings.Join(s.lib.ParamNames(), ", "))
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var b strings.Builder
	fmt.Fprintf(&b, "# Parameter %s\n\n%s\n\n", name, def.Hint)
	fmt.Fprintf(&b, "- Type: %s\n- Source: %s\n", def.Type, def.Source)
	if def.Optional {
		b.WriteString("- Optional: renders as empty when unknown\n")
	}
	r := s.resolve(name, nil, map[string]bool{})
	if r.value != "" {
		fmt.Fprintf(&b, "- Current value: `%s` (%s)\n", r.value, r.source)
	} else {
		b.WriteString("- Current value: unknown\n")
	}
	if r.stale != "" {
		fmt.Fprintf(&b, "- Stale: %s\n", r.stale)
	}
	if def.Default != "" {
		fmt.Fprintf(&b, "- Defaults to: %s\n", def.Default)
	}
	for _, d := range def.Derive {
		fmt.Fprintf(&b, "- Derived from: %s (%s)\n", d.From, d.Func)
	}
	if p := s.lib.providers(name); len(p) > 0 {
		fmt.Fprintf(&b, "- Provided by: %s\n", strings.Join(p, ", "))
	}
	if c := s.lib.consumers(name); len(c) > 0 {
		fmt.Fprintf(&b, "- Used by: %s\n", strings.Join(c, ", "))
	}
	if def.Guide != "" {
		guide := def.Guide
		if s.cfg.LibraryDir != "" {
			guide = filepath.Join(s.cfg.LibraryDir, filepath.FromSlash(def.Guide))
		}
		fmt.Fprintf(&b, "- Guide: `%s`\n", guide)
	}
	return b.String(), nil
}

// Markdown summarizes a run for the investigator, including at most maxRows
// result rows (all when maxRows <= 0).
func (r *RunResult) Markdown(maxRows int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Ran %s: %d rows.", r.Query, len(r.Rows))
	if r.File != "" {
		fmt.Fprintf(&b, " Full output (README, query, all rows) written to `%s`.", r.File)
	}
	b.WriteString("\n\nParameters:\n")
	for _, st := range r.Params {
		if st.Config {
			continue
		}
		if st.Value == "" {
			fmt.Fprintf(&b, "- %s: (unset)\n", st.Name)
			continue
		}
		fmt.Fprintf(&b, "- %s = `%s` (%s)\n", st.Name, st.Value, st.Source)
	}
	for _, param := range sortedKeys(r.Provided) {
		fmt.Fprintf(&b, "\nProvided %s = `%s`; remembered for later queries.", param, r.Provided[param])
	}
	for _, param := range sortedKeys(r.Ambiguous) {
		fmt.Fprintf(&b, "\nThe result holds several values for %s, so none was remembered: %s. Pass the right one explicitly.",
			param, strings.Join(r.Ambiguous[param], ", "))
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "\nWarning: %s", w)
	}
	fmt.Fprintf(&b, "\n\n## Query\n\n```kql\n%s\n```\n\n## Results\n\n", strings.TrimRight(r.Rendered, "\n"))
	if len(r.Rows) == 0 {
		b.WriteString("No results returned.\n")
		return b.String()
	}
	rows := r.Rows
	if maxRows > 0 && len(rows) > maxRows {
		rows = rows[:maxRows]
	}
	resultRows := make([]resultRow, 0, len(rows))
	for _, row := range rows {
		resultRows = append(resultRows, resultRow{columns: r.Columns, values: row})
	}
	b.WriteString(renderMarkdownTable(resultRows))
	if len(rows) < len(r.Rows) {
		fmt.Fprintf(&b, "\n(showing %d of %d rows — read the output file for the rest, or narrow the query with kusto_query)\n", len(rows), len(r.Rows))
	}
	return b.String()
}
