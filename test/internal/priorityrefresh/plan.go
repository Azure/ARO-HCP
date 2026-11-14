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

package priorityrefresh

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"math"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"

	"github.com/Azure/ARO-HCP/test/pkg/citests"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

// Change is one mapped priority edit in one source file.
type Change struct {
	Name               string   `json:"name"`
	Path               string   `json:"path"`
	Line               int      `json:"line"`
	OldPriority        int      `json:"old_priority"`
	NewPriority        int      `json:"new_priority"`
	ScoreMinutes       float64  `json:"score_minutes"`
	EligibleBuildCount int      `json:"eligible_build_count"`
	Environments       []string `json:"environments"`
}

// Summary is the machine-readable outcome of planning or applying a change set.
type Summary struct {
	Changes      []Change `json:"changes"`
	Unchanged    []string `json:"unchanged"`
	Insufficient []string `json:"insufficient"`
	Unmatched    []string `json:"unmatched"`
	Applied      bool     `json:"applied"`
}

// fileChange is the complete replacement of one source file.
type fileChange struct {
	context     *fileContext
	edits       []textEdit
	replacement []byte
}

// ChangeSet is the process-local plan produced by Plan and consumed by Apply.
type ChangeSet struct {
	repo         string
	files        []*fileChange
	changes      []Change
	unchanged    []string
	insufficient []string
	unmatched    []string
}

// Len returns the number of source edits in the change set.
func (c ChangeSet) Len() int { return len(c.changes) }

// Summary renders the plan, or the applied result when applied is true.
func (c ChangeSet) Summary(applied bool) Summary {
	summary := Summary{
		Changes:      append([]Change{}, c.changes...),
		Unchanged:    sortedStrings(c.unchanged),
		Insufficient: sortedStrings(c.insufficient),
		Unmatched:    sortedStrings(c.unmatched),
		Applied:      applied,
	}
	if summary.Changes == nil {
		summary.Changes = []Change{}
	}
	return summary
}

// planner resolves priority proposals against one checkout.
type planner struct {
	repo         string
	catalog      map[string]*et.ExtensionTestSpec
	files        map[string]*fileContext
	resolved     map[string]*sourceCall
	resolveErr   map[string]error
	locations    map[string][]string
	changes      []Change
	unchanged    []string
	insufficient []string
	unmatched    []string
}

// Plan maps every scored proposal onto an exact StartPriority call. Nothing is
// written, and one unmappable proposal aborts the whole batch.
func Plan(repo string, catalog et.ExtensionTestSpecs, report citests.PriorityReport) (ChangeSet, error) {
	planner, err := newPlanner(repo, catalog, report)
	if err != nil {
		return ChangeSet{}, err
	}
	set := ChangeSet{
		repo:         planner.repo,
		changes:      planner.changes,
		unchanged:    planner.unchanged,
		insufficient: planner.insufficient,
		unmatched:    planner.unmatched,
	}
	planned := make(map[string]Change, len(set.changes))
	for _, change := range set.changes {
		planned[change.Name] = change
	}
	if err := planner.checkSharedTargets(planned); err != nil {
		return ChangeSet{}, err
	}
	files, err := planner.materialize(planned)
	if err != nil {
		return ChangeSet{}, err
	}
	set.files = files
	sort.Slice(set.changes, func(i, j int) bool {
		if set.changes[i].Path != set.changes[j].Path {
			return set.changes[i].Path < set.changes[j].Path
		}
		if set.changes[i].Line != set.changes[j].Line {
			return set.changes[i].Line < set.changes[j].Line
		}
		return set.changes[i].Name < set.changes[j].Name
	})
	return set, nil
}

// Expected derives the expected priorities from the before catalog and the
// report alone. It never reads or maps sources, so it is safe to run against an
// already modified checkout.
func Expected(catalog et.ExtensionTestSpecs, report citests.PriorityReport) (ChangeSet, error) {
	if err := validateReport(report); err != nil {
		return ChangeSet{}, err
	}
	if err := ensureCatalogEntries(catalog); err != nil {
		return ChangeSet{}, err
	}
	specs := make(map[string]*et.ExtensionTestSpec, len(catalog))
	for _, spec := range catalog {
		if _, exists := specs[spec.Name]; exists {
			return ChangeSet{}, fmt.Errorf("catalog contains duplicate spec name %q", spec.Name)
		}
		specs[spec.Name] = spec
	}
	changes, unchanged, insufficient, unmatched, err := classifyReport(specs, report)
	if err != nil {
		return ChangeSet{}, err
	}
	return ChangeSet{changes: changes, unchanged: unchanged, insufficient: insufficient, unmatched: unmatched}, nil
}

// classifyReport decides which report rows change a label, using only the catalog.
func classifyReport(catalog map[string]*et.ExtensionTestSpec, report citests.PriorityReport) ([]Change, []string, []string, []string, error) {
	var changes []Change
	var unchanged, insufficient, unmatched []string
	for _, row := range report.Tests {
		switch {
		case row.Name == "":
			unmatched = append(unmatched, row.TestID)
			continue
		case row.Status != citests.StatusScored || row.ProposedPriority == nil || row.ScoreMinutes == nil:
			insufficient = append(insufficient, row.Name)
			continue
		}
		spec, ok := catalog[row.Name]
		if !ok {
			unmatched = append(unmatched, row.Name)
			continue
		}
		current, err := specPriority(spec)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		// An explicit StartPriority:0 label is a declaration the zero proposal
		// must remove, while an absent label already matches a zero proposal.
		if current == *row.ProposedPriority && !(current == 0 && declaresPriority(spec)) {
			unchanged = append(unchanged, row.Name)
			continue
		}
		changes = append(changes, Change{
			Name:               row.Name,
			OldPriority:        current,
			NewPriority:        *row.ProposedPriority,
			ScoreMinutes:       *row.ScoreMinutes,
			EligibleBuildCount: contributingBuilds(row),
			Environments:       contributingEnvironments(row),
		})
	}
	return changes, unchanged, insufficient, unmatched, nil
}

// newPlanner builds a planner and resolves a source location for every proposal.
func newPlanner(repo string, catalog et.ExtensionTestSpecs, report citests.PriorityReport) (*planner, error) {
	if err := validateReport(report); err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(repo)
	if err != nil {
		return nil, fmt.Errorf("resolve repository path: %w", err)
	}
	planner := &planner{
		repo:       absolute,
		catalog:    map[string]*et.ExtensionTestSpec{},
		files:      map[string]*fileContext{},
		resolved:   map[string]*sourceCall{},
		resolveErr: map[string]error{},
		locations:  map[string][]string{},
	}
	if err := ensureCatalogEntries(catalog); err != nil {
		return nil, err
	}
	for _, spec := range catalog {
		if _, exists := planner.catalog[spec.Name]; exists {
			return nil, fmt.Errorf("catalog contains duplicate spec name %q", spec.Name)
		}
		planner.catalog[spec.Name] = spec
		for _, location := range spec.CodeLocations {
			relPath, line, ok := canonicalLocation(location)
			if !ok {
				continue
			}
			key := relPath + ":" + strconv.Itoa(line)
			planner.locations[key] = append(planner.locations[key], spec.Name)
		}
	}
	// Resolve the whole catalog so a shared table-level priority always sees
	// every descendant, not only the specs that carry a proposal.
	planner.resolveAll()

	changes, unchanged, insufficient, unmatched, err := classifyReport(planner.catalog, report)
	if err != nil {
		return nil, err
	}
	planner.unchanged, planner.insufficient, planner.unmatched = unchanged, insufficient, unmatched
	for _, change := range changes {
		call, err := planner.resolveSpec(change.Name)
		if err != nil {
			return nil, fmt.Errorf("map %q: %w", change.Name, err)
		}
		// A numeric comparison alone cannot tell an absent decorator from an
		// explicit StartPriority(0), so the declaration must agree on presence.
		declared := declaresPriority(planner.catalog[change.Name])
		if call.value != change.OldPriority || (call.node != nil) != declared {
			return nil, fmt.Errorf("stale mapping for %q: catalog declares priority %d (label present: %t) but %s:%d declares %d (decorator present: %t); regenerate the catalog",
				change.Name, change.OldPriority, declared, call.file.relPath, call.file.fset.Position(callPos(call)).Line, call.value, call.node != nil)
		}
		change.Path = call.file.relPath
		change.Line = call.file.fset.Position(callPos(call)).Line
		if call.node != nil {
			change.Line = call.file.fset.Position(call.node.Pos()).Line
		}
		edit, err := call.edit(change.NewPriority)
		if err != nil {
			return nil, fmt.Errorf("edit %q: %w", change.Name, err)
		}
		call.file.edits = append(call.file.edits, edit)
		planner.changes = append(planner.changes, change)
	}
	if err := planner.validateUnchanged(); err != nil {
		return nil, err
	}
	return planner, nil
}

// validateUnchanged checks every unchanged scored row whose declaration still
// resolves, so a stale source cannot be reported as unchanged while other files
// are written. An unmappable unchanged row stays reported: it plans no edit.
func (p *planner) validateUnchanged() error {
	for _, name := range p.unchanged {
		spec, ok := p.catalog[name]
		if !ok {
			continue
		}
		call, err := p.resolveSpec(name)
		if err != nil {
			continue
		}
		declared := declaresPriority(spec)
		if call.value != specPriorityValue(spec) || (call.node != nil) != declared {
			return fmt.Errorf("stale mapping for %q: catalog declares priority %d (label present: %t) but %s:%d declares %d (decorator present: %t); regenerate the catalog",
				name, specPriorityValue(spec), declared, call.file.relPath, call.file.fset.Position(callPos(call)).Line, call.value, call.node != nil)
		}
	}
	return nil
}

// specPriorityValue returns the catalog priority of a validated spec.
func specPriorityValue(spec *et.ExtensionTestSpec) int {
	priority, _ := specPriority(spec)
	return priority
}

// editTarget identifies the exact source position a plan writes to.
func editTarget(call *sourceCall) string {
	if call.node != nil {
		return call.file.relPath + ":" + strconv.Itoa(call.file.offset(call.node.Pos()))
	}
	return call.file.relPath + ":insert:" + strconv.Itoa(call.insertAt)
}

// declaresPriority reports whether the spec carries an explicit priority label.
func declaresPriority(spec *et.ExtensionTestSpec) bool {
	for label := range spec.Labels {
		if strings.HasPrefix(label, labels.StartPriorityLabelPrefix) {
			return true
		}
	}
	return false
}

// callPos returns the node whose line best identifies the edited priority.
func callPos(call *sourceCall) token.Pos {
	if call.node != nil {
		return call.node.Pos()
	}
	return call.leaf.Pos()
}

// ensureCatalogEntries rejects catalogs with null entries, which would
// otherwise dereference a nil spec while planning or verifying.
func ensureCatalogEntries(catalog et.ExtensionTestSpecs) error {
	for index, spec := range catalog {
		if spec == nil {
			return fmt.Errorf("catalog entry %d is null", index)
		}
	}
	return nil
}

// validateReport rejects reports this consumer cannot act on.
func validateReport(report citests.PriorityReport) error {
	if report.SchemaVersion != citests.ReportSchemaVersion {
		return fmt.Errorf("unsupported report schema_version %d (supported: %d)", report.SchemaVersion, citests.ReportSchemaVersion)
	}
	if report.Algorithm != citests.AlgorithmEnvironmentNormalizedMean {
		return fmt.Errorf("unsupported report algorithm %q (supported: %s)", report.Algorithm, citests.AlgorithmEnvironmentNormalizedMean)
	}
	testIDs := map[string]struct{}{}
	names := map[string]struct{}{}
	for _, row := range report.Tests {
		if row.TestID == "" {
			return fmt.Errorf("report contains a row without a test_id")
		}
		if _, ok := testIDs[row.TestID]; ok {
			return fmt.Errorf("report contains duplicate test_id %q", row.TestID)
		}
		testIDs[row.TestID] = struct{}{}
		if row.Name != "" {
			if _, ok := names[row.Name]; ok {
				return fmt.Errorf("report contains duplicate test name %q", row.Name)
			}
			names[row.Name] = struct{}{}
		}
		switch row.Status {
		case citests.StatusScored:
			if row.ProposedPriority == nil || row.ScoreMinutes == nil {
				return fmt.Errorf("scored row %q has no proposal", row.Name)
			}
			if *row.ProposedPriority < 0 || *row.ProposedPriority > labels.StartPriorityMax {
				return fmt.Errorf("row %q proposes priority %d outside 0..%d", row.Name, *row.ProposedPriority, labels.StartPriorityMax)
			}
			if math.IsNaN(*row.ScoreMinutes) || math.IsInf(*row.ScoreMinutes, 0) || *row.ScoreMinutes < 0 {
				return fmt.Errorf("row %q has invalid score %v", row.Name, *row.ScoreMinutes)
			}
		case citests.StatusInsufficientData:
			if row.ProposedPriority != nil || row.ScoreMinutes != nil {
				return fmt.Errorf("insufficient row %q carries a proposal", row.Name)
			}
		default:
			return fmt.Errorf("row %q has unknown status %q", row.Name, row.Status)
		}
	}
	return nil
}

// specPriority reads the catalog's declared start priority for a spec.
func specPriority(spec *et.ExtensionTestSpec) (int, error) {
	var declared []string
	for label := range spec.Labels {
		if strings.HasPrefix(label, labels.StartPriorityLabelPrefix) {
			declared = append(declared, label)
		}
	}
	switch len(declared) {
	case 0:
		return 0, nil
	case 1:
	default:
		sort.Strings(declared)
		return 0, fmt.Errorf("spec %q declares multiple start priorities (%s)", spec.Name, strings.Join(declared, ", "))
	}
	value := strings.TrimPrefix(declared[0], labels.StartPriorityLabelPrefix)
	priority, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("spec %q declares non-integer start priority %q", spec.Name, value)
	}
	if priority < 0 || priority > labels.StartPriorityMax {
		return 0, fmt.Errorf("spec %q declares start priority %d outside 0..%d", spec.Name, priority, labels.StartPriorityMax)
	}
	return priority, nil
}

// canonicalLocation normalizes a catalog code location to a repository-relative
// path under test/e2e plus a positive line number.
func canonicalLocation(location string) (string, int, bool) {
	separator := strings.LastIndex(location, ":")
	if separator < 0 {
		return "", 0, false
	}
	line, err := strconv.Atoi(location[separator+1:])
	if err != nil || line <= 0 {
		return "", 0, false
	}
	path := location[:separator]
	if filepath.IsAbs(path) {
		marker := "/" + e2eDirectory + "/"
		index := strings.LastIndex(path, marker)
		if index < 0 {
			return "", 0, false
		}
		path = e2eDirectory + "/" + path[index+len(marker):]
	}
	cleaned := filepath.ToSlash(filepath.Clean(strings.TrimPrefix(path, "./")))
	if !strings.HasPrefix(cleaned, e2eDirectory+"/") || strings.Contains(cleaned, "..") || !strings.HasSuffix(cleaned, ".go") {
		return "", 0, false
	}
	return cleaned, line, true
}

// resolveSpec finds the single source declaration for a catalog spec.
func (p *planner) resolveSpec(name string) (*sourceCall, error) {
	if call, ok := p.resolved[name]; ok {
		return call, nil
	}
	if err, ok := p.resolveErr[name]; ok {
		return nil, err
	}
	spec, ok := p.catalog[name]
	if !ok {
		return nil, fmt.Errorf("spec is not in the catalog")
	}
	var found *sourceCall
	var tried []string
	for _, location := range spec.CodeLocations {
		relPath, line, ok := canonicalLocation(location)
		if !ok {
			continue
		}
		tried = append(tried, relPath+":"+strconv.Itoa(line))
		file, err := p.load(relPath)
		if err != nil {
			return nil, err
		}
		leaf, ok := file.locateAt(line, name)
		if !ok {
			continue
		}
		if found != nil && found.leaf != leaf {
			return nil, fmt.Errorf("spec has more than one matching source declaration (%s:%d)", file.relPath, line)
		}
		call, err := file.resolve(leaf)
		if err != nil {
			return nil, err
		}
		found = call
	}
	if found == nil {
		return nil, fmt.Errorf("no It or Entry declaration matches the recorded code locations %v", tried)
	}
	p.resolved[name] = found
	return found, nil
}

// resolveAll resolves every catalog spec, keeping per-spec errors so only specs
// that a proposal depends on can abort the batch.
func (p *planner) resolveAll() {
	for _, name := range sortedKeys(p.catalog) {
		if _, err := p.resolveSpec(name); err != nil {
			p.resolveErr[name] = err
		}
	}
}

// load returns a cached parse of a repository-relative source file.
func (p *planner) load(relPath string) (*fileContext, error) {
	if file, ok := p.files[relPath]; ok {
		return file, nil
	}
	absPath := filepath.Join(p.repo, filepath.FromSlash(relPath))
	if !strings.HasPrefix(absPath, filepath.Join(p.repo, filepath.FromSlash(e2eDirectory))+string(filepath.Separator)) {
		return nil, fmt.Errorf("%s escapes the %s subtree", relPath, e2eDirectory)
	}
	if err := ensureNoSymlinkComponents(p.repo, absPath); err != nil {
		return nil, err
	}
	file, err := loadFile(p.repo, absPath)
	if err != nil {
		return nil, err
	}
	p.files[relPath] = file
	return file, nil
}

// edit produces the byte edit for one new priority.
func (c *sourceCall) edit(priority int) (textEdit, error) {
	f := c.file
	if c.node == nil {
		qualifier := f.labelsName + "."
		if f.dotImport {
			qualifier = ""
		}
		return textEdit{
			start: c.insertAt,
			end:   c.insertAt,
			text:  fmt.Sprintf(", %sStartPriority(%d)", qualifier, priority),
		}, nil
	}
	literal := c.node.Args[0]
	if priority != 0 {
		return textEdit{start: f.offset(literal.Pos()), end: f.offset(literal.End()), text: strconv.Itoa(priority)}, nil
	}
	call := c.leaf
	if c.onTable {
		call = c.table
	}
	index := -1
	for i, argument := range call.Args {
		if argument == c.node {
			index = i
			break
		}
	}
	if index <= 0 {
		return textEdit{}, fmt.Errorf("cannot remove StartPriority outside a declaration's decorator list")
	}
	previousEnd := f.offset(call.Args[index-1].End())
	argumentEnd := f.offset(c.node.End())
	comma := bytes.IndexByte(f.source[previousEnd:argumentEnd], ',')
	if comma < 0 {
		return textEdit{}, fmt.Errorf("cannot find the separator before StartPriority")
	}
	start := previousEnd + comma
	if f.commentBetween(start, argumentEnd) {
		return textEdit{}, fmt.Errorf("%s:%d removing StartPriority would delete a comment; drop the decorator by hand",
			f.relPath, f.fset.Position(c.node.Pos()).Line)
	}
	return textEdit{start: start, end: argumentEnd, text: ""}, nil
}

// checkSharedTargets refuses to rewrite a table-level priority whose descendants
// do not all propose the same target.
func (p *planner) checkSharedTargets(planned map[string]Change) error {
	// Two specs can resolve to one edit target: a table decorator, and a leaf
	// declared once inside a loop whose name varies per iteration.
	sharing := map[string][]string{}
	for name, call := range p.resolved {
		sharing[editTarget(call)] = append(sharing[editTarget(call)], name)
	}
	for name := range planned {
		call, err := p.resolveSpec(name)
		if err != nil {
			continue
		}
		line := call.file.fset.Position(callPos(call)).Line
		// An unresolvable descendant still shares the table when its recorded
		// locations point at the table declaration, so both views are required.
		// Catalog locations identify the DescribeTable call, never the decorator.
		candidates := append([]string{}, sharing[editTarget(call)]...)
		if call.onTable {
			// An unresolvable descendant still shares the table when its recorded
			// locations point at the table declaration, so both views are required.
			// Catalog locations identify the DescribeTable call, never the decorator.
			tableLine := call.file.fset.Position(call.table.Pos()).Line
			key := call.file.relPath + ":" + strconv.Itoa(tableLine)
			candidates = append(candidates, p.locations[key]...)
		}
		for _, candidate := range candidates {
			if _, resolved := p.resolved[candidate]; !resolved {
				return fmt.Errorf("shared-priority-conflict: %s:%d is shared by %q, which cannot be resolved: %v",
					call.file.relPath, line, candidate, p.resolveErr[candidate])
			}
			change, ok := planned[candidate]
			if !ok {
				return fmt.Errorf("shared-priority-conflict: %s:%d is shared by %q, which has no proposal for priority %d",
					call.file.relPath, line, candidate, planned[name].NewPriority)
			}
			if change.NewPriority != planned[name].NewPriority {
				return fmt.Errorf("shared-priority-conflict: %s:%d is shared by %q proposing %d and %q proposing %d",
					call.file.relPath, line, name, planned[name].NewPriority, candidate, change.NewPriority)
			}
		}
	}
	return nil
}

// materialize applies every file's edits, formats the result and verifies that
// each planned declaration ended up with exactly its proposed priority.
func (p *planner) materialize(planned map[string]Change) ([]*fileChange, error) {
	var files []*fileChange
	for _, relPath := range sortedKeys(p.files) {
		file := p.files[relPath]
		if len(file.edits) == 0 {
			continue
		}
		replacement, err := file.materialize()
		if err != nil {
			return nil, err
		}
		formatted, err := format.Source(replacement)
		if err != nil {
			return nil, fmt.Errorf("format %s after edits: %w", file.relPath, err)
		}
		if err := p.verifyFile(file, formatted, planned); err != nil {
			return nil, err
		}
		files = append(files, &fileChange{context: file, edits: file.edits, replacement: formatted})
	}
	return files, nil
}

// materialize applies the queued byte edits in descending offset order.
func (f *fileContext) materialize() ([]byte, error) {
	edits := append([]textEdit(nil), f.edits...)
	sort.Slice(edits, func(i, j int) bool {
		if edits[i].start != edits[j].start {
			return edits[i].start < edits[j].start
		}
		return edits[i].end < edits[j].end
	})
	// A table-level priority shared by several planned specs queues one edit per
	// spec; identical edits collapse, conflicting ones abort.
	unique := edits[:0]
	for i, edit := range edits {
		if i > 0 && edit.start == edits[i-1].start && edit.end == edits[i-1].end {
			if edit.text != edits[i-1].text {
				return nil, fmt.Errorf("conflicting edits in %s at offset %d", f.relPath, edit.start)
			}
			continue
		}
		unique = append(unique, edit)
	}
	edits = unique
	for i := 1; i < len(edits); i++ {
		if edits[i].start < edits[i-1].end {
			return nil, fmt.Errorf("overlapping edits in %s", f.relPath)
		}
	}
	result := append([]byte(nil), f.source...)
	for i := len(edits) - 1; i >= 0; i-- {
		if edits[i].start < 0 || edits[i].end > len(result) || edits[i].start > edits[i].end {
			return nil, fmt.Errorf("invalid edit range in %s", f.relPath)
		}
		result = append(result[:edits[i].start], append([]byte(edits[i].text), result[edits[i].end:]...)...)
	}
	return result, nil
}

// verifyFile reparses an edited file and checks every planned declaration.
func (p *planner) verifyFile(original *fileContext, source []byte, planned map[string]Change) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, original.absPath, source, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("reparse %s after edits: %w", original.relPath, err)
	}
	edited := &fileContext{absPath: original.absPath, relPath: original.relPath, source: source,
		fset: fset, file: file, parents: map[ast.Node]ast.Node{}, labelsName: original.labelsName, dotImport: original.dotImport}
	buildParents(file, edited.parents)
	edited.indexLeaves()
	for _, change := range planned {
		if change.Path != original.relPath {
			continue
		}
		leaf, ok := edited.locateByName(change.Name)
		if !ok {
			return fmt.Errorf("edited %s no longer declares %q", original.relPath, change.Name)
		}
		call, err := edited.resolve(leaf)
		if err != nil {
			return fmt.Errorf("re-resolve %q after edits: %w", change.Name, err)
		}
		if call.value != change.NewPriority {
			return fmt.Errorf("edited %s declares priority %d for %q, want %d", original.relPath, call.value, change.Name, change.NewPriority)
		}
	}
	if err := importStillUsed(fset, file, original.dotImport); err != nil {
		return fmt.Errorf("%s after edits: %w", original.relPath, err)
	}
	return nil
}

// importStillUsed reports whether an edited file still references the labels
// package. Identifiers are resolved by scope, so a local declaration that
// reuses the import name is never mistaken for a package reference.
func importStillUsed(fset *token.FileSet, file *ast.File, dotImport bool) error {
	if _, imported := labelsImportSpec(file); !imported {
		return nil
	}
	if dotImport {
		return fmt.Errorf("the labels package is dot-imported, so its remaining use cannot be verified; edit the declaration by hand")
	}
	info := &types.Info{Uses: map[*ast.Ident]types.Object{}}
	config := types.Config{Importer: stubImporter{}, Error: func(error) {}, DisableUnusedImportCheck: true}
	_, _ = config.Check("edited", fset, []*ast.File{file}, info)
	for _, object := range info.Uses {
		name, ok := object.(*types.PkgName)
		if ok && name.Imported().Path() == labelsImportPath {
			return nil
		}
	}
	return fmt.Errorf("removing the last StartPriority would leave the %s import unused; edit the declaration by hand", labelsImportPath)
}

// labelsImportSpec returns the import spec of the repository labels package.
func labelsImportSpec(file *ast.File) (*ast.ImportSpec, bool) {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err == nil && path == labelsImportPath {
			return spec, true
		}
	}
	return nil, false
}

// stubImporter gives scope analysis an empty package for every import, which is
// enough to attribute identifiers to an import without loading dependencies.
type stubImporter struct{}

// Import returns an empty package named after the import path.
func (stubImporter) Import(path string) (*types.Package, error) {
	stub := types.NewPackage(path, pathpkg.Base(path))
	stub.MarkComplete()
	return stub, nil
}

// contributingBuilds totals the distinct builds of the sufficient strata.
func contributingBuilds(row citests.TestReport) int {
	total := 0
	for _, environment := range row.Environments {
		if environment.Sufficient {
			total += environment.EligibleBuildCount
		}
	}
	return total
}

// contributingEnvironments lists the sufficient strata in a stable order.
func contributingEnvironments(row citests.TestReport) []string {
	environments := []string{}
	for _, environment := range row.Environments {
		if environment.Sufficient {
			environments = append(environments, environment.Environment)
		}
	}
	sort.Strings(environments)
	return environments
}

// sortedKeys returns map keys in sorted order.
func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// sortedStrings returns a sorted copy.
func sortedStrings(values []string) []string {
	copied := append([]string{}, values...)
	sort.Strings(copied)
	return copied
}
