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
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"text/template"
	"text/template/parse"

	"sigs.k8s.io/yaml"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// The query library describes every embedded query declaratively so it can be
// rendered and executed on demand (dynamic investigations) instead of only by
// the snapshot gatherer's hard-coded dependency chain:
//
//   - queries/params.yaml is the global catalog of template parameters: their
//     type, where their value comes from, a hint for discovering it, and how it
//     may be derived from other parameters.
//   - queries/<component>/<queryName>/query.yaml describes one query: what it
//     shows, which database it targets, which parameters it needs, and which
//     parameter values its results provide to other queries.
//   - queries/README.md and queries/guides/*.md are handwritten guidance for
//     investigators.

const (
	// libraryParamsFile is the global parameter catalog.
	libraryParamsFile = queriesRoot + "/params.yaml"
	// libraryQueryFile is the per-query description file name.
	libraryQueryFile = "query.yaml"
	// LibraryOverviewFile is the handwritten overview of the query library,
	// relative to the library root.
	LibraryOverviewFile = "README.md"
)

// Parameter types.
const (
	ParamTypeString   = "string"
	ParamTypeInt      = "int"
	ParamTypeDatetime = "datetime"
)

// Parameter sources.
const (
	// ParamSourceAgent parameters are supplied by the investigator (or
	// provided by queries, derived, or defaulted).
	ParamSourceAgent = "agent"
	// ParamSourceConfig parameters are fixed by the CLI configuration.
	ParamSourceConfig = "config"
)

// Database names used by query descriptions, mapped to the parameter holding
// the concrete database name.
var libraryDatabases = map[string]string{
	"service":          "ServiceDatabase",
	"hcp":              "HCPDatabase",
	"monitoringEvents": "MonitoringEventsDatabase",
}

// ParamDef describes one template parameter in queries/params.yaml.
type ParamDef struct {
	// Name is the template variable name (the map key in params.yaml).
	Name string `json:"-"`
	// Type is string (default), int or datetime.
	Type string `json:"type,omitempty"`
	// Source is agent (default) or config.
	Source string `json:"source,omitempty"`
	// Hint explains what the value is and how to discover it.
	Hint string `json:"hint"`
	// Optional parameters render as empty values when unknown.
	Optional bool `json:"optional,omitempty"`
	// Default names another parameter whose value is used when this one is unknown.
	Default string `json:"default,omitempty"`
	// Derive lists ways to compute the value from other parameters, tried in order.
	Derive []DeriveRule `json:"derive,omitempty"`
	// Guide is a library-relative path of a guide explaining the parameter.
	Guide string `json:"guide,omitempty"`
}

// DeriveRule computes a parameter from another parameter with a named function.
type DeriveRule struct {
	From string `json:"from"`
	Func string `json:"func"`
}

// paramsFile is the schema of queries/params.yaml.
type paramsFile struct {
	Params map[string]*ParamDef `json:"params"`
}

// QueryParam references a parameter from a query description. In YAML it is
// either a bare parameter name or an object overriding the catalog entry (or
// declaring a parameter local to the query).
type QueryParam struct {
	Name     string `json:"name"`
	Hint     string `json:"hint,omitempty"`
	Type     string `json:"type,omitempty"`
	Optional *bool  `json:"optional,omitempty"`
}

// UnmarshalJSON accepts either a string or an object.
func (p *QueryParam) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		p.Name = name
		return nil
	}
	type plain QueryParam
	return json.Unmarshal(data, (*plain)(p))
}

// Provide describes how a query result provides a parameter value: either
// from a result column, or from a Go template over the row's columns.
type Provide struct {
	Column   string `json:"column,omitempty"`
	Template string `json:"template,omitempty"`

	tmpl *template.Template
}

// QueryDef describes one query in queries/<component>/<queryName>/query.yaml.
type QueryDef struct {
	// Name is "<component>/<queryName>".
	Name string `json:"-"`
	// Description is a one-line summary.
	Description string `json:"description"`
	// Database is service, hcp or monitoringEvents.
	Database string `json:"database"`
	// Tags help find the query.
	Tags []string `json:"tags,omitempty"`
	// Params lists every template variable the query uses.
	Params []QueryParam `json:"params"`
	// RequireAnyOf lists alternative groups of parameters: at least one group
	// must be fully known. Parameters in a group are otherwise optional.
	RequireAnyOf [][]string `json:"requireAnyOf,omitempty"`
	// Provides maps parameter names to the result values that provide them.
	Provides map[string]*Provide `json:"provides,omitempty"`
	// Notes holds additional guidance (for example applicable resource types).
	Notes string `json:"notes,omitempty"`

	templatePath string
	readmePath   string
}

// QueryLibrary is the parsed declarative query library.
type QueryLibrary struct {
	params  map[string]*ParamDef
	queries map[string]*QueryDef
	names   []string
}

// deriveFuncs are the functions available to params.yaml derive rules. Each
// returns "" when the value cannot be derived from the input.
var deriveFuncs = map[string]func(string) string{
	"armSubscriptionID": func(id string) string {
		if r := parseARMResourceID(id); r != nil {
			return r.SubscriptionID
		}
		return ""
	},
	"armResourceGroup": func(id string) string {
		if r := parseARMResourceID(id); r != nil {
			return r.ResourceGroupName
		}
		return ""
	},
	"armResourceType": func(id string) string {
		if r := parseARMResourceID(id); r != nil && r.ResourceType.Namespace != "" {
			return r.ResourceType.String()
		}
		return ""
	},
	"armName": func(id string) string {
		if r := parseARMResourceID(id); r != nil {
			return r.Name
		}
		return ""
	},
	"armClusterResourceID":        armClusterResourceID,
	"serviceProviderResourceType": serviceProviderResourceType,
}

// parseARMResourceID parses an ARM resource ID, returning nil when invalid.
func parseARMResourceID(id string) *azcorearm.ResourceID {
	r, err := azcorearm.ParseResourceID(strings.TrimSpace(id))
	if err != nil {
		return nil
	}
	return r
}

// armClusterResourceID returns the ID of the HCP cluster that is, or is an
// ancestor of, the given ARM resource ID.
func armClusterResourceID(id string) string {
	for r := parseARMResourceID(id); r != nil; r = r.Parent {
		if strings.EqualFold(r.ResourceType.String(), "microsoft.redhatopenshift/hcpopenshiftclusters") {
			return r.String()
		}
	}
	return ""
}

// LoadQueryLibrary parses the embedded declarative query library and
// validates it.
func LoadQueryLibrary() (*QueryLibrary, error) {
	raw, err := queriesFS.ReadFile(libraryParamsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", libraryParamsFile, err)
	}
	var pf paramsFile
	if err := yaml.UnmarshalStrict(raw, &pf); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", libraryParamsFile, err)
	}
	lib := &QueryLibrary{params: pf.Params, queries: map[string]*QueryDef{}}
	for name, p := range lib.params {
		p.Name = name
		if p.Type == "" {
			p.Type = ParamTypeString
		}
		if p.Source == "" {
			p.Source = ParamSourceAgent
		}
		if err := lib.validateParam(p); err != nil {
			return nil, fmt.Errorf("%s: parameter %s: %w", libraryParamsFile, name, err)
		}
	}

	queryFiles, err := fs.Glob(queriesFS, queriesRoot+"/*/*/"+libraryQueryFile)
	if err != nil {
		return nil, err
	}
	for _, f := range queryFiles {
		dir := path.Dir(f)
		raw, err := queriesFS.ReadFile(f)
		if err != nil {
			return nil, err
		}
		q := &QueryDef{}
		if err := yaml.UnmarshalStrict(raw, q); err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", f, err)
		}
		q.Name = strings.TrimPrefix(dir, queriesRoot+"/")
		q.templatePath = dir + "/query.kql"
		q.readmePath = dir + "/README.md"
		if err := lib.validateQuery(q); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		lib.queries[q.Name] = q
		lib.names = append(lib.names, q.Name)
	}
	sort.Strings(lib.names)
	return lib, nil
}

func (l *QueryLibrary) validateParam(p *ParamDef) error {
	switch p.Type {
	case ParamTypeString, ParamTypeInt, ParamTypeDatetime:
	default:
		return fmt.Errorf("unknown type %q", p.Type)
	}
	switch p.Source {
	case ParamSourceAgent, ParamSourceConfig:
	default:
		return fmt.Errorf("unknown source %q", p.Source)
	}
	if p.Hint == "" {
		return fmt.Errorf("missing hint")
	}
	if p.Default != "" && l.params[p.Default] == nil {
		return fmt.Errorf("default refers to unknown parameter %q", p.Default)
	}
	for _, d := range p.Derive {
		if l.params[d.From] == nil {
			return fmt.Errorf("derive refers to unknown parameter %q", d.From)
		}
		if deriveFuncs[d.Func] == nil {
			return fmt.Errorf("derive uses unknown function %q", d.Func)
		}
	}
	return nil
}

func (l *QueryLibrary) validateQuery(q *QueryDef) error {
	if q.Description == "" {
		return fmt.Errorf("missing description")
	}
	if _, ok := libraryDatabases[q.Database]; !ok {
		return fmt.Errorf("unknown database %q", q.Database)
	}
	if _, err := queriesFS.ReadFile(q.templatePath); err != nil {
		return fmt.Errorf("missing query.kql: %w", err)
	}
	seen := map[string]bool{}
	for _, p := range q.Params {
		if p.Name == "" {
			return fmt.Errorf("parameter without a name")
		}
		if seen[p.Name] {
			return fmt.Errorf("parameter %s listed twice", p.Name)
		}
		seen[p.Name] = true
		if l.params[p.Name] == nil {
			if p.Hint == "" {
				return fmt.Errorf("local parameter %s must have a hint", p.Name)
			}
			switch p.Type {
			case "", ParamTypeString, ParamTypeInt, ParamTypeDatetime:
			default:
				return fmt.Errorf("local parameter %s has unknown type %q", p.Name, p.Type)
			}
		} else if p.Type != "" {
			return fmt.Errorf("parameter %s: type is defined by params.yaml", p.Name)
		}
	}
	for _, group := range q.RequireAnyOf {
		if len(group) == 0 {
			return fmt.Errorf("empty requireAnyOf group")
		}
		for _, name := range group {
			if !seen[name] {
				return fmt.Errorf("requireAnyOf parameter %s is not listed in params", name)
			}
		}
	}
	for name, pv := range q.Provides {
		if l.params[name] == nil {
			return fmt.Errorf("provides unknown parameter %s", name)
		}
		if l.params[name].Source == ParamSourceConfig {
			return fmt.Errorf("provides config parameter %s", name)
		}
		if (pv.Column == "") == (pv.Template == "") {
			return fmt.Errorf("provides %s: set exactly one of column or template", name)
		}
		if pv.Template != "" {
			t, err := template.New(name).Option("missingkey=error").Parse(pv.Template)
			if err != nil {
				return fmt.Errorf("provides %s: %w", name, err)
			}
			pv.tmpl = t
		}
	}
	return nil
}

// Param returns the catalog entry for a parameter, or nil.
func (l *QueryLibrary) Param(name string) *ParamDef {
	return l.params[name]
}

// Query returns a query description, or nil.
func (l *QueryLibrary) Query(name string) *QueryDef {
	return l.queries[name]
}

// QueryNames returns every query name, sorted.
func (l *QueryLibrary) QueryNames() []string {
	return append([]string(nil), l.names...)
}

// ParamNames returns every catalog parameter name, sorted.
func (l *QueryLibrary) ParamNames() []string {
	names := make([]string, 0, len(l.params))
	for n := range l.params {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// providers returns the queries that provide a parameter, sorted.
func (l *QueryLibrary) providers(param string) []string {
	var out []string
	for _, n := range l.names {
		if _, ok := l.queries[n].Provides[param]; ok {
			out = append(out, n)
		}
	}
	return out
}

// consumers returns the queries that use a parameter, sorted.
func (l *QueryLibrary) consumers(param string) []string {
	var out []string
	for _, n := range l.names {
		if l.queries[n].param(param) != nil {
			out = append(out, n)
		}
	}
	return out
}

// param returns the query's reference to a parameter, or nil.
func (q *QueryDef) param(name string) *QueryParam {
	for i := range q.Params {
		if q.Params[i].Name == name {
			return &q.Params[i]
		}
	}
	return nil
}

// inAnyOfGroup reports whether a parameter is part of a requireAnyOf group.
func (q *QueryDef) inAnyOfGroup(name string) bool {
	for _, g := range q.RequireAnyOf {
		for _, n := range g {
			if n == name {
				return true
			}
		}
	}
	return false
}

// paramType returns the effective type of one of the query's parameters.
func (l *QueryLibrary) paramType(q *QueryDef, name string) string {
	if def := l.params[name]; def != nil {
		return def.Type
	}
	if p := q.param(name); p != nil && p.Type != "" {
		return p.Type
	}
	return ParamTypeString
}

// paramHint returns the effective hint of one of the query's parameters.
func (l *QueryLibrary) paramHint(q *QueryDef, name string) string {
	if p := q.param(name); p != nil && p.Hint != "" {
		return p.Hint
	}
	if def := l.params[name]; def != nil {
		return def.Hint
	}
	return ""
}

// paramOptional reports whether one of the query's parameters may be unknown
// on its own (it is optional, or only required as part of a requireAnyOf group).
func (l *QueryLibrary) paramOptional(q *QueryDef, name string) bool {
	if q.inAnyOfGroup(name) {
		return true
	}
	if p := q.param(name); p != nil && p.Optional != nil {
		return *p.Optional
	}
	if def := l.params[name]; def != nil {
		return def.Optional
	}
	return false
}

// readme returns the query's README.
func (q *QueryDef) readme() string {
	data, err := queriesFS.ReadFile(q.readmePath)
	if err != nil {
		return ""
	}
	return string(data)
}

// templateFields returns every template variable referenced by an embedded
// query template, including through partial templates it invokes.
func templateFields(templatePath string) ([]string, error) {
	tmpl, err := parseQueryTemplate(templatePath)
	if err != nil {
		return nil, err
	}
	return collectTemplateFields(tmpl), nil
}

// collectTemplateFields returns the sorted top-level field names referenced by
// a parsed template, following {{ template }} invocations.
func collectTemplateFields(tmpl *template.Template) []string {
	fields := map[string]bool{}
	visited := map[string]bool{}
	var walk func(n parse.Node)
	walkBranch := func(b *parse.BranchNode) {
		walk(b.Pipe)
		walk(b.List)
		if b.ElseList != nil {
			walk(b.ElseList)
		}
	}
	walk = func(n parse.Node) {
		switch n := n.(type) {
		case *parse.ListNode:
			if n == nil {
				return
			}
			for _, c := range n.Nodes {
				walk(c)
			}
		case *parse.ActionNode:
			walk(n.Pipe)
		case *parse.IfNode:
			walkBranch(&n.BranchNode)
		case *parse.RangeNode:
			walkBranch(&n.BranchNode)
		case *parse.WithNode:
			walkBranch(&n.BranchNode)
		case *parse.TemplateNode:
			if n.Pipe != nil {
				walk(n.Pipe)
			}
			if !visited[n.Name] {
				visited[n.Name] = true
				if t := tmpl.Lookup(n.Name); t != nil && t.Tree != nil {
					walk(t.Root)
				}
			}
		case *parse.PipeNode:
			if n == nil {
				return
			}
			for _, c := range n.Cmds {
				walk(c)
			}
		case *parse.CommandNode:
			for _, a := range n.Args {
				walk(a)
			}
		case *parse.FieldNode:
			fields[n.Ident[0]] = true
		case *parse.ChainNode:
			walk(n.Node)
		}
	}
	walk(tmpl.Root)
	out := make([]string, 0, len(fields))
	for f := range fields {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// values computes the distinct non-empty values a result provides for one
// parameter, in first-appearance order. Template provides skip rows in which
// any referenced column is empty.
func (p *Provide) values(columns []string, rows [][]string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, row := range rows {
		var v string
		if p.Column != "" {
			idx := -1
			for i, c := range columns {
				if c == p.Column {
					idx = i
				}
			}
			if idx < 0 {
				return nil, fmt.Errorf("result has no column %q", p.Column)
			}
			if idx < len(row) {
				v = row[idx]
			}
		} else {
			data := map[string]string{}
			for i, c := range columns {
				if i < len(row) {
					data[c] = row[i]
				}
			}
			complete := true
			for _, f := range collectTemplateFields(p.tmpl) {
				if strings.TrimSpace(data[f]) == "" {
					complete = false
				}
			}
			if !complete {
				continue
			}
			var buf bytes.Buffer
			if err := p.tmpl.Execute(&buf, data); err != nil {
				return nil, err
			}
			v = buf.String()
		}
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out, nil
}
