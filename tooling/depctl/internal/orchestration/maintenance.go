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

package orchestration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/evidence"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
)

// MaxInvestigationBytes bounds how much failing log text is inspected.
const MaxInvestigationBytes = 8 << 20

// MaintenanceOptions control the read-only assessment.
type MaintenanceOptions struct {
	// Investigate collects failure evidence for an investigate decision.
	Investigate bool
	// BaseSHA is the merge base used to compute changed-file evidence.
	BaseSHA string
	// ChangedPaths supplies changed-file evidence; nil records it as missing.
	ChangedPaths func(ctx context.Context, base, head string) ([]string, error)
	// ComparisonURLs are candidate trusted comparison runs.
	ComparisonURLs []string
	// EvidenceDir receives retrieved artifacts.
	EvidenceDir string
	// StateDir holds durable per-head investigation budgets.
	StateDir string
}

// ErrNoForge reports a command that needs remote read evidence.
var ErrNoForge = errors.New("remote read adapters are not configured")

// MaintenanceInspect assesses one owned proposal without mutating anything.
func (e *Engine) MaintenanceInspect(ctx context.Context, repository string, number int, options MaintenanceOptions) (model.MaintenanceDecision, error) {
	if e.deps.Forge == nil {
		return model.MaintenanceDecision{}, ErrNoForge
	}
	pull, err := e.deps.Forge.PullRequest(ctx, number)
	if err != nil {
		return model.MaintenanceDecision{}, err
	}
	snapshot, err := e.deps.Forge.Snapshot(ctx, number, pull.HeadSHA)
	if err != nil {
		return model.MaintenanceDecision{}, err
	}
	decision, err := policy.Assess(repository, number, pull.HeadSHA, snapshot)
	if err != nil {
		return model.MaintenanceDecision{}, err
	}
	if options.Investigate && decision.Action == "investigate" {
		investigation := e.Investigate(ctx, snapshot, decision, options)
		decision.Investigation = &investigation
		switch investigation.Diagnosis {
		case model.DiagnosisCandidateRegression:
			decision.Action = "report-regression"
			decision.ActionReason = "failure evidence references a candidate-changed path"
		case model.DiagnosisUnrelatedFailure:
			decision.Action = "recover-retry"
			decision.ActionReason = "a trusted comparison run shows the same failure signature"
		default:
			decision.Action = "investigate"
			decision.ActionReason = "evidence is incomplete; the hold is preserved and the investigation resumes with new evidence"
		}
	}
	if d, err := e.loadInvestigationState(options.StateDir, pull.HeadSHA); err == nil && d != nil {
		decision.ActionReason = decision.ActionReason + fmt.Sprintf(" (attempt %d)", d.Attempt)
	}
	return decision, nil
}

// Investigate collects evidence for one failed current head, read-only.
//
// It never treats a retrieval failure as a diagnosis, never reuses an unrelated
// run as a comparison, and records exactly which evidence is missing.
func (e *Engine) Investigate(ctx context.Context, snapshot forge.Snapshot, decision model.MaintenanceDecision, options MaintenanceOptions) model.Investigation {
	investigation := model.Investigation{
		Head:   decision.Head,
		Marker: decision.RecoveryMarker,
	}
	if recorded, err := e.loadInvestigationState(options.StateDir, decision.Head); err == nil && recorded != nil {
		investigation.Attempt = recorded.Attempt
	}
	investigation.Attempt++

	if options.ChangedPaths == nil || options.BaseSHA == "" {
		investigation.Missing = append(investigation.Missing, "changed-file evidence: no base revision available")
	} else {
		paths, err := options.ChangedPaths(ctx, options.BaseSHA, decision.Head)
		if err != nil {
			investigation.Missing = append(investigation.Missing, "changed-file evidence: "+err.Error())
		} else {
			investigation.ChangedPaths = paths
			investigation.SourceBase = options.BaseSHA
		}
	}
	if len(investigation.ChangedPaths) == 0 && len(investigation.Missing) == 0 {
		investigation.Missing = append(investigation.Missing, "changed-file evidence: the change set is empty")
	}

	var failing []model.BlockedCheck
	for _, check := range decision.CI.Blocked {
		if check.URL != "" {
			failing = append(failing, check)
		}
	}
	if len(failing) == 0 {
		investigation.Missing = append(investigation.Missing, "no current-head failing CI URL")
	}
	logContent := ""
	for _, check := range failing {
		item, content := e.collectFailureEvidence(ctx, check, options.EvidenceDir)
		investigation.Evidence = append(investigation.Evidence, item...)
		if content != "" {
			logContent = content
		}
	}

	signature := Signature(logContent)
	if signature != "" {
		investigation.Rationale = append(investigation.Rationale, "failure signature: "+signature)
	}
	for _, candidate := range options.ComparisonURLs {
		comparison := e.evaluateComparison(ctx, candidate, signature, options.EvidenceDir)
		investigation.Comparisons = append(investigation.Comparisons, comparison)
	}

	switch {
	case len(investigation.Missing) > 0 && signature == "":
		investigation.Diagnosis = model.DiagnosisUnknownIncomplete
	case signature != "" && referencesChangedPath(signature, logContent, investigation.ChangedPaths):
		investigation.Diagnosis = model.DiagnosisCandidateRegression
		investigation.Rationale = append(investigation.Rationale, "the failure references a path the candidate changed")
	case anyAccepted(investigation.Comparisons):
		investigation.Diagnosis = model.DiagnosisUnrelatedFailure
		investigation.Rationale = append(investigation.Rationale, "a trusted comparison run with the same signature passed")
	default:
		investigation.Diagnosis = model.DiagnosisUnknownIncomplete
		investigation.Missing = append(investigation.Missing, "trusted passing comparison for this failure signature")
	}
	sort.Strings(investigation.Missing)
	investigation.ResumableFrom = fmt.Sprintf("head=%s attempt=%d missing=%d", decision.Head, investigation.Attempt, len(investigation.Missing))
	e.saveInvestigationState(options.StateDir, investigation)
	return investigation
}

// Retrieves the artifacts of one failing check.
func (e *Engine) collectFailureEvidence(ctx context.Context, check model.BlockedCheck, directory string) ([]model.EvidenceItem, string) {
	if _, err := evidence.ArtifactBase(check.URL); err != nil {
		return []model.EvidenceItem{{
			Name: check.Name, URL: check.URL, Status: "unsupported",
			Error: "only canonical public Prow run URLs are eligible",
		}}, ""
	}
	var items []model.EvidenceItem
	target := filepath.Join(directory, fmt.Sprintf("%s-%s", sanitizeName(check.Name), check.URL[strings.LastIndex(check.URL, "/")+1:]))
	manifest, err := e.deps.Evidence.Fetch(ctx, check.URL, target)
	if err != nil {
		item := model.EvidenceItem{Name: check.Name, URL: check.URL, Status: "failed", Error: err.Error()}
		if len(manifest.Missing) > 0 {
			item.Error = item.Error + "; missing: " + strings.Join(manifest.Missing, ", ")
		}
		return append(items, item), ""
	}
	content := ""
	for _, file := range manifest.Files {
		items = append(items, model.EvidenceItem{
			Name: file.Name, URL: check.URL, Path: file.Path, Digest: file.Digest,
			Status: "retrieved", Bytes: file.Bytes,
		})
		if file.Name == "build-log.txt" {
			content = readBounded(file.Path)
		}
	}
	return items, content
}

// Fetches a candidate comparison run and judges it.
func (e *Engine) evaluateComparison(ctx context.Context, location, signature, directory string) model.Comparison {
	comparison := model.Comparison{URL: location}
	if _, err := evidence.ArtifactBase(location); err != nil {
		comparison.Reason = "not a canonical public Prow run URL"
		return comparison
	}
	if signature == "" {
		comparison.Reason = "no failure signature to compare"
		return comparison
	}
	target := filepath.Join(directory, "comparison-"+sanitizeName(location[strings.LastIndex(location, "/")+1:]))
	manifest, err := e.deps.Evidence.Fetch(ctx, location, target)
	if err != nil {
		comparison.Reason = "comparison evidence could not be retrieved: " + err.Error()
		return comparison
	}
	if !strings.EqualFold(manifest.Result, "SUCCESS") {
		comparison.Reason = fmt.Sprintf("comparison run result is %q, not SUCCESS", manifest.Result)
		return comparison
	}
	for _, file := range manifest.Files {
		if file.Name != "build-log.txt" {
			continue
		}
		if strings.Contains(Signature(readBounded(file.Path)), signature) {
			comparison.Accepted = true
			comparison.Reason = "same failure signature on a passing trusted run"
			return comparison
		}
	}
	comparison.Reason = "the comparison log does not contain the failure signature"
	return comparison
}

var (
	signatureLine = regexp.MustCompile(`(?i)(error|failed|failure|panic|timed out|not synchronized)`)
	volatileToken = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

// Signature returns a normalized failure signature from a build log.
func Signature(logContent string) string {
	if logContent == "" {
		return ""
	}
	var selected []string
	for _, line := range strings.Split(logContent, "\n") {
		if len(selected) == 3 {
			break
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || !signatureLine.MatchString(trimmed) {
			continue
		}
		selected = append(selected, normalizeLine(trimmed))
	}
	joined := strings.Join(selected, " | ")
	if len(joined) > 300 {
		joined = joined[:300]
	}
	return joined
}

// Removes volatile prefixes so that the same failure compares.
func normalizeLine(line string) string {
	fields := strings.Fields(line)
	var kept []string
	for _, field := range fields {
		trimmed := strings.Trim(field, "()[],.;:<>\"'")
		if strings.Contains(trimmed, "T") && strings.HasSuffix(trimmed, "Z") {
			continue
		}
		if volatileToken.MatchString(trimmed) {
			continue
		}
		kept = append(kept, trimmed)
	}
	normalized := strings.Join(kept, " ")
	if len(normalized) > 160 {
		normalized = normalized[:160]
	}
	return normalized
}

// Reports whether the failure text names a changed path.
func referencesChangedPath(signature, logContent string, changed []string) bool {
	haystack := signature + "\n" + logContent
	for _, path := range changed {
		if path == "" {
			continue
		}
		if strings.Contains(haystack, path) {
			return true
		}
		base := filepath.Base(path)
		if len(base) > 4 && strings.Contains(haystack, base) {
			return true
		}
	}
	return false
}

// Reports whether any comparison was accepted.
func anyAccepted(comparisons []model.Comparison) bool {
	for _, comparison := range comparisons {
		if comparison.Accepted {
			return true
		}
	}
	return false
}

// Reads a bounded prefix of a file.
func readBounded(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, MaxInvestigationBytes))
	if err != nil && len(content) == 0 {
		return ""
	}
	return string(content)
}

// Replaces path characters for an evidence directory name.
func sanitizeName(value string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", " ", "-", ":", "-", ".", "-")
	return replacer.Replace(value)
}

// investigationState is the durable per-head investigation record.
type investigationState struct {
	Heads map[string]model.Investigation `json:"heads"`
}

// Loads the durable investigation record for a head.
func (e *Engine) loadInvestigationState(directory, head string) (*model.Investigation, error) {
	if directory == "" {
		return nil, nil
	}
	journal, err := e.openJournal(directory)
	if err != nil {
		return nil, err
	}
	var stored investigationState
	if _, err := journal.LoadState("investigations", &stored); err != nil {
		return nil, err
	}
	record, ok := stored.Heads[head]
	if !ok {
		return nil, nil
	}
	return &record, nil
}

// Stores the durable investigation record for a head.
func (e *Engine) saveInvestigationState(directory string, investigation model.Investigation) {
	if directory == "" {
		return
	}
	journal, err := e.openJournal(directory)
	if err != nil {
		return
	}
	var stored investigationState
	_, _ = journal.LoadState("investigations", &stored)
	if stored.Heads == nil {
		stored.Heads = map[string]model.Investigation{}
	}
	stored.Heads[investigation.Head] = investigation
	_ = journal.SaveState("investigations", stored)
}
