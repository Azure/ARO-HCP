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

// Package model defines the versioned records exchanged between depctl phases.
//
// Records carry evidence only. A digest or a self-reported field never grants
// authority: the trusted gate re-reads remote state before any mutation.
package model

import "github.com/Azure/ARO-HCP/tooling/depctl/internal/schema"

// Record kinds.
const (
	KindInventory         = "Inventory"
	KindPlan              = "Plan"
	KindCandidate         = "Candidate"
	KindValidation        = "Validation"
	KindProposal          = "Proposal"
	KindPublication       = "PublicationReceipt"
	KindMaintenance       = "MaintenanceDecision"
	KindRunResult         = "RunResult"
	KindContinuation      = "ContinuationDecision"
	KindEvidenceManifest  = "EvidenceManifest"
	KindSimulationJournal = "SimulationJournal"
)

// Outcomes reported in a RunResult. An outcome is not a version state and not a
// CI state; those are reported separately.
const (
	OutcomePrepared              = "prepared"
	OutcomeNoChange              = "no_change"
	OutcomeWaiting               = "waiting"
	OutcomeBlocked               = "blocked"
	OutcomeStale                 = "stale"
	OutcomeFailed                = "failed"
	OutcomeInterrupted           = "interrupted"
	OutcomeCompleted             = "completed"
	OutcomeAwaitingAuthorization = "awaiting_authorization"
)

// Phases of the lifecycle state machine.
const (
	PhaseDiscover      = "discover"
	PhaseReconcile     = "reconcile"
	PhasePlan          = "plan"
	PhasePrepare       = "prepare_candidate"
	PhaseValidate      = "validate"
	PhaseProposalReady = "proposal_ready"
	PhaseAwaitAuth     = "await_authorization"
	PhaseGate          = "gate"
	PhasePublish       = "publish"
	PhaseObserveCI     = "observe_ci"
	PhaseMaintain      = "maintain"
	PhaseCompleted     = "completed"
)

// Version states of one family or pin.
const (
	VersionCurrent  = "current"
	VersionOutdated = "outdated"
	VersionAhead    = "ahead"
	VersionBlocked  = "blocked"
	VersionError    = "error"
)

// EvidenceScope distinguishes what a run actually observed.
const (
	ScopeLocalOnly      = "local_only"
	ScopeRemoteResolved = "remote_resolved"
)

// Consumer statuses.
const (
	ConsumerPassed = "passed"
	ConsumerFailed = "failed"
	ConsumerNotRun = "not_run"
	ConsumerError  = "error"
)

// Validation verdicts.
const (
	VerdictValidated       = "validated"
	VerdictRegression      = "candidate_regression"
	VerdictBaselineBlocked = "baseline_blocked"
	VerdictIncomplete      = "incomplete"
)

// Diagnosis classes for failure investigation. Confidence is never evidence.
const (
	DiagnosisCandidateRegression = "candidate_regression"
	DiagnosisUnrelatedFailure    = "supported_unrelated_failure"
	DiagnosisUnknownIncomplete   = "unknown_incomplete"
)

// Family kinds match the current inventory helper.
const (
	FamilyGitHubRelease = "github-release"
	FamilyGoModule      = "go-module"
)

// Inventory is the read-only picture of pinned versions.
type Inventory struct {
	schema.Envelope
	Repository    string   `json:"repository"`
	BaseSHA       string   `json:"baseSHA"`
	Profile       string   `json:"profile"`
	EvidenceScope string   `json:"evidenceScope"`
	ObservedAt    string   `json:"observedAt"`
	Complete      bool     `json:"complete"`
	Errors        []string `json:"errors"`
	Families      []Family `json:"families"`
}

// Family groups every pin of one upstream dependency.
type Family struct {
	Family    string   `json:"family"`
	Kind      string   `json:"kind"`
	SourceURL string   `json:"sourceURL"`
	ToolNames []string `json:"toolNames"`
	Pins      []Pin    `json:"pins"`
	Current   string   `json:"current,omitempty"`
	Latest    string   `json:"latest,omitempty"`
	Status    string   `json:"status"`
	Reason    string   `json:"reason,omitempty"`
}

// Pin records one repository location that pins an upstream version.
type Pin struct {
	ToolName string `json:"toolName"`
	Source   string `json:"source"`
	Variable string `json:"variable,omitempty"`
	Current  string `json:"current,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Plan pins the exact targets and the consumers that must validate a candidate.
type Plan struct {
	schema.Envelope
	PlanID        string          `json:"planID"`
	Repository    string          `json:"repository"`
	BaseSHA       string          `json:"baseSHA"`
	Profile       string          `json:"profile"`
	PolicyDigest  string          `json:"policyDigest"`
	CreatedAt     string          `json:"createdAt"`
	Outcome       string          `json:"outcome"`
	EvidenceScope string          `json:"evidenceScope"`
	Families      []PlannedFamily `json:"families"`
	Consumers     []ConsumerRef   `json:"consumers"`
	Blockers      []string        `json:"blockers"`
}

// PlannedFamily names one approved target version and the pins it rewrites.
type PlannedFamily struct {
	Family  string      `json:"family"`
	Kind    string      `json:"kind"`
	From    string      `json:"from"`
	To      string      `json:"to"`
	Targets []PinTarget `json:"targets"`
}

// PinTarget is a single edit site and its intended value.
type PinTarget struct {
	ToolName string `json:"toolName"`
	Source   string `json:"source"`
	Variable string `json:"variable,omitempty"`
	From     string `json:"from"`
	To       string `json:"to"`
}

// ConsumerRef refers to a registered consumer by fixed ID with validated
// parameters. Plans cannot inject commands.
type ConsumerRef struct {
	ID     string            `json:"id"`
	Params map[string]string `json:"params,omitempty"`
}

// Candidate describes the isolated tree produced by apply.
type Candidate struct {
	schema.Envelope
	CandidateID  string            `json:"candidateID"`
	PlanID       string            `json:"planID"`
	BaseSHA      string            `json:"baseSHA"`
	Workspace    string            `json:"workspace"`
	TreeDigest   string            `json:"treeDigest"`
	DiffDigest   string            `json:"diffDigest"`
	ChangedPaths []string          `json:"changedPaths"`
	Generated    []GeneratedPath   `json:"generated"`
	Tools        []ToolIdentity    `json:"tools"`
	FileDigests  map[string]string `json:"fileDigests"`
}

// GeneratedPath records which producer wrote a generated path.
type GeneratedPath struct {
	Path     string `json:"path"`
	Producer string `json:"producer"`
}

// ToolIdentity records the exact executable that produced or checked an artifact.
type ToolIdentity struct {
	ID      string `json:"id"`
	Version string `json:"version,omitempty"`
	Source  string `json:"source,omitempty"`
}

// Validation binds every required consumer result to a plan and candidate.
type Validation struct {
	schema.Envelope
	PlanID      string           `json:"planID"`
	CandidateID string           `json:"candidateID"`
	Verdict     string           `json:"verdict"`
	Complete    bool             `json:"complete"`
	Results     []ConsumerResult `json:"results"`
	NotRun      []string         `json:"notRun"`
}

// ConsumerResult is one baseline or candidate consumer execution.
type ConsumerResult struct {
	ID         string `json:"id"`
	Phase      string `json:"phase"`
	Status     string `json:"status"`
	ExitCode   int    `json:"exitCode"`
	DurationMS int64  `json:"durationMS"`
	StartedAt  string `json:"startedAt"`
	LogPath    string `json:"logPath,omitempty"`
	LogDigest  string `json:"logDigest,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// Proposal packages a validated candidate. It confers no authorization.
type Proposal struct {
	schema.Envelope
	ProposalID          string           `json:"proposalID"`
	PlanID              string           `json:"planID"`
	CandidateID         string           `json:"candidateID"`
	ValidationID        string           `json:"validationID"`
	GroupID             string           `json:"groupID"`
	Repository          string           `json:"repository"`
	Base                string           `json:"base"`
	Branch              string           `json:"branch"`
	Title               string           `json:"title"`
	Body                string           `json:"body"`
	Labels              []string         `json:"labels"`
	Markers             []string         `json:"markers"`
	SourceRefs          []SourceRef      `json:"sourceRefs"`
	DiffDigest          string           `json:"diffDigest"`
	PatchPath           string           `json:"patchPath"`
	PermittedOperations []string         `json:"permittedOperations"`
	Expected            StateExpectation `json:"expected"`
	RiskNotes           []string         `json:"riskNotes"`
}

// SourceRef identifies an upstream source proposal covered by a replacement.
type SourceRef struct {
	Number  int    `json:"number"`
	HeadSHA string `json:"headSHA"`
}

// StateExpectation is the remote state a publisher must observe before writing.
type StateExpectation struct {
	BranchExists  bool   `json:"branchExists"`
	BranchHeadSHA string `json:"branchHeadSHA,omitempty"`
	BaseSHA       string `json:"baseSHA"`
}

// PublicationReceipt distinguishes attempted from remotely confirmed effects.
type PublicationReceipt struct {
	schema.Envelope
	OperationKey      string   `json:"operationKey"`
	Repository        string   `json:"repository"`
	Branch            string   `json:"branch"`
	CommitSHA         string   `json:"commitSHA,omitempty"`
	PullRequestNumber int      `json:"pullRequestNumber,omitempty"`
	PullRequestURL    string   `json:"pullRequestURL,omitempty"`
	ObservedAuthor    string   `json:"observedAuthor,omitempty"`
	ObservedHeadRepo  string   `json:"observedHeadRepo,omitempty"`
	ObservedBase      string   `json:"observedBase,omitempty"`
	Confirmed         bool     `json:"confirmed"`
	Unfinished        []string `json:"unfinished"`
	ObservedAt        string   `json:"observedAt"`
}

// MaintenanceDecision is the read-only result of assessing an owned pull request.
type MaintenanceDecision struct {
	schema.Envelope
	Repository        string         `json:"repository"`
	PullRequest       int            `json:"pullRequest"`
	ExpectedHead      string         `json:"expectedHead"`
	Head              string         `json:"head"`
	Kind              string         `json:"kind"`
	State             string         `json:"state"`
	Draft             bool           `json:"draft"`
	Merged            bool           `json:"merged"`
	Held              bool           `json:"held"`
	ActiveHolds       []ActiveHold   `json:"activeHolds"`
	CI                CIEvidence     `json:"ci"`
	ReleaseStatus     string         `json:"releaseStatus"`
	CanReleaseHold    bool           `json:"canReleaseHold"`
	CanRecoverRetry   bool           `json:"canRecoverRetry"`
	CanRefreshUpgrade bool           `json:"canRefreshUpgrade"`
	ReleaseBlockers   []string       `json:"releaseBlockers"`
	RetryBlockers     []string       `json:"retryBlockers"`
	RefreshBlockers   []string       `json:"refreshBlockers"`
	RecoveryMarker    string         `json:"recoveryMarker"`
	Investigation     *Investigation `json:"investigation,omitempty"`
	AllowedActions    []string       `json:"allowedActions"`
	Action            string         `json:"action"`
	ActionReason      string         `json:"actionReason"`
}

// ActiveHold is one hold command still in force.
type ActiveHold struct {
	CommentID int    `json:"commentID"`
	Author    string `json:"author"`
	Kind      string `json:"kind"`
	SHA       string `json:"sha,omitempty"`
}

// CIEvidence separates positive checks from failing, pending or unknown ones.
type CIEvidence struct {
	Positive []string       `json:"positive"`
	Blocked  []BlockedCheck `json:"blocked"`
}

// BlockedCheck is a non-success check or status on the expected head.
type BlockedCheck struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	URL     string `json:"url,omitempty"`
	Pending bool   `json:"pending"`
}

// Investigation is the bounded, read-only failure investigation record.
type Investigation struct {
	Attempt       int            `json:"attempt"`
	Marker        string         `json:"marker"`
	Head          string         `json:"head"`
	ChangedPaths  []string       `json:"changedPaths"`
	SourceBase    string         `json:"sourceBase,omitempty"`
	Evidence      []EvidenceItem `json:"evidence"`
	Comparisons   []Comparison   `json:"comparisons"`
	Diagnosis     string         `json:"diagnosis"`
	Rationale     []string       `json:"rationale"`
	Missing       []string       `json:"missing"`
	ResumableFrom string         `json:"resumableFrom,omitempty"`
}

// EvidenceItem is one retrieval attempt for a failure artifact.
type EvidenceItem struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Path   string `json:"path,omitempty"`
	Digest string `json:"digest,omitempty"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Bytes  int64  `json:"bytes,omitempty"`
}

// Comparison is a candidate trusted comparison run and how it was judged.
type Comparison struct {
	URL      string `json:"url"`
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason"`
}

// Artifact is a bounded output file referenced by a run result.
type Artifact struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Digest string `json:"digest,omitempty"`
}

// FamilySummary reports the selected or skipped families of a run.
type FamilySummary struct {
	Family string `json:"family"`
	Status string `json:"status"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// RunResult is the terminal record of one operation.
type RunResult struct {
	schema.Envelope
	Operation     string                `json:"operation"`
	Profile       string                `json:"profile"`
	Repository    string                `json:"repository"`
	BaseSHA       string                `json:"baseSHA"`
	PolicyDigest  string                `json:"policyDigest"`
	RunID         string                `json:"runID"`
	PlanID        string                `json:"planID,omitempty"`
	CandidateID   string                `json:"candidateID,omitempty"`
	Complete      bool                  `json:"complete"`
	Outcome       string                `json:"outcome"`
	Phase         string                `json:"phase"`
	EvidenceScope string                `json:"evidenceScope"`
	Families      []FamilySummary       `json:"families"`
	Blockers      []string              `json:"blockers"`
	Errors        []string              `json:"errors"`
	Artifacts     []Artifact            `json:"artifacts"`
	Publication   *PublicationReceipt   `json:"publication,omitempty"`
	Maintenance   *MaintenanceDecision  `json:"maintenance,omitempty"`
	Continuation  *ContinuationDecision `json:"continuation,omitempty"`
	Actions       []string              `json:"actions"`
}

// ContinuationDecision is the typed, trusted-event continuation policy result.
type ContinuationDecision struct {
	schema.Envelope
	Repository  string `json:"repository"`
	PullRequest int    `json:"pullRequest"`
	Event       string `json:"event"`
	Eligible    bool   `json:"eligible"`
	Reason      string `json:"reason"`
	Target      string `json:"target"`
}

// EvidenceManifest describes retrieved public Prow evidence.
type EvidenceManifest struct {
	schema.Envelope
	ProwURL      string         `json:"prowURL"`
	ArtifactBase string         `json:"artifactBase"`
	Result       string         `json:"result"`
	Files        []EvidenceFile `json:"files"`
	Complete     bool           `json:"complete"`
	Missing      []string       `json:"missing"`
}

// EvidenceFile is one retrieved artifact.
type EvidenceFile struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	Digest string `json:"digest"`
}
