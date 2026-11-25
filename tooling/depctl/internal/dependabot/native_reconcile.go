// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package dependabot

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/go-github/v69/github"
	"golang.org/x/sys/unix"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
	automation "github.com/Azure/ARO-HCP/tooling/github"
)

// NativeActionRequest binds one of four guarded actions to an exact PR head.
type NativeActionRequest struct {
	number, replacement    int
	head, action, evidence string
}

var (
	nativeRecoveryDirective   = regexp.MustCompile(`(?m)^[\s\p{Z}\x{85}]*/|@[A-Za-z0-9]`)
	nativeRecoveryLink        = regexp.MustCompile(`https://[^\s\p{Z}\x{85})<>]+`)
	nativeRecoveryTrustedLink = regexp.MustCompile(`^https://(?:prow\.ci\.openshift\.org|gcsweb-ci\.apps\.ci\.l2s4\.p1\.openshiftapps\.com|gcs\.ci\.openshift\.org|storage\.googleapis\.com|github\.com)/`)
)

// ReadNativeActionRequest validates the safe-output envelope and action without accessing GitHub.
func ReadNativeActionRequest(path string) (*NativeActionRequest, error) {
	path = filepath.Clean(path)
	directory, err := unix.Open(filepath.Dir(path), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open native output directory: %w", err)
	}
	defer unix.Close(directory)
	envelope, _, err := nativeReadOutputEnvelope(directory, filepath.Base(path))
	if err != nil {
		return nil, err
	}
	selected, payload, err := nativeSelectOutput(envelope, "native_action", "request")
	if err != nil || selected == nil {
		return nil, err
	}
	if err := nativePublicationJSONUnicode([]byte(payload)); err != nil {
		return nil, fmt.Errorf("validate native action Unicode: %w", err)
	}
	value, err := nativeDecode([]byte(payload))
	if err != nil {
		return nil, fmt.Errorf("decode native action request: %w", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, nativeReject("native request must be a JSON object")
	}
	for key := range object {
		switch key {
		case "number", "expected_head_sha", "action", "evidence", "replacement_pr":
		default:
			return nil, nativeReject("invalid action schema")
		}
	}
	number, numberOK := nativeInteger(object["number"])
	head, _ := object["expected_head_sha"].(string)
	action, _ := object["action"].(string)
	if !numberOK || number <= 0 || int64(int(number)) != number || !nativeSHA.MatchString(head) {
		return nil, nativeReject("native action requires a positive PR number and full lowercase head SHA")
	}
	request := &NativeActionRequest{number: int(number), head: head, action: action}
	if evidence, exists := object["evidence"]; exists {
		request.evidence, ok = evidence.(string)
		if !ok {
			return nil, nativeReject("invalid recovery evidence")
		}
	}
	replacement, exists := object["replacement_pr"]
	if action == "close-superseded" {
		pr, ok := nativeInteger(replacement)
		if !ok || pr <= 0 || pr == number || int64(int(pr)) != pr {
			return nil, nativeReject("merged replacement PR required")
		}
		request.replacement = int(pr)
	}
	if exists && action != "close-superseded" {
		return nil, nativeReject("replacement is only supported for closure")
	}
	switch action {
	case "admit", "release-hold", "close-superseded":
	case "recover-retry":
		if length := utf8.RuneCountInString(request.evidence); length < policy.MinRecoveryEvidence || length > policy.MaxRecoveryEvidence {
			return nil, nativeReject("diagnosed recovery evidence required")
		}
		if nativeRecoveryDirective.MatchString(request.evidence) || strings.Contains(request.evidence, nativePublicationMarker) || strings.Contains(request.evidence, policy.RecoveryMarker) {
			return nil, nativeReject("recovery evidence contains commands, mentions or markers")
		}
	default:
		return nil, nativeReject("unsupported native action")
	}
	return request, nil
}

// Reconcile replays authorization on fresh evidence and issues only one fixed-command comment.
func (p *NativePublisher) Reconcile(ctx context.Context, request *NativeActionRequest) (map[string]any, error) {
	if request == nil {
		return map[string]any{"status": "no-request"}, nil
	}
	if request.number <= 0 || p.writer == nil {
		return nil, nativeReject("native reconcile requires a validated request and injected writer")
	}
	var body string
	for range 2 {
		var status string
		var err error
		body, status, err = p.nativeActionComment(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("authorize native %s on #%d: %w", request.action, request.number, err)
		}
		if status != "" {
			return map[string]any{"number": request.number, "status": status}, nil
		}
	}
	comment, _, err := p.writer.Issues.CreateComment(ctx, p.owner, p.repo, request.number, &github.IssueComment{Body: &body})
	if err != nil {
		return nil, fmt.Errorf("post native action comment; inspect before retry: %w", err)
	}
	if comment.GetID() <= 0 || comment.GetBody() != body {
		return nil, nativeReject("comment response incomplete; inspect before retry")
	}
	return map[string]any{"number": request.number, "comment_id": comment.GetID(), "action": request.action, "status": "requested"}, nil
}

// NativeActionComment authorizes the exact command against a complete current snapshot.
func (p *NativePublisher) nativeActionComment(ctx context.Context, request *NativeActionRequest) (body, status string, err error) {
	pull, err := p.pullDetail(ctx, request.number)
	if err != nil {
		return "", "", err
	}
	author := pull.GetUser().GetLogin()
	switch author {
	case nativePublicationBot:
	case nativePublicationRobot:
		if request.action != "recover-retry" && request.action != "release-hold" {
			return "", "", nativeReject("only recovery actions support verified robot replacements")
		}
	default:
		return "", "", nativeReject("foreign action target")
	}
	snapshot, err := p.nativePublicationSnapshot(ctx, request.number, request.head, author)
	if err != nil {
		return "", "", err
	}
	var intent map[string]any
	if author == nativePublicationRobot {
		if _, err := p.validateReplacement(snapshot.pull, false, false); err != nil {
			return "", "", err
		}
	}
	if author == nativePublicationBot {
		intent, err = p.nativePublicationSourceIntent(ctx, snapshot)
		if err != nil {
			return "", "", fmt.Errorf("derive native source intent: %w", err)
		}
	}
	ci, err := nativeSnapshotCI(snapshot)
	if err != nil {
		return "", "", fmt.Errorf("evaluate current-head CI: %w", err)
	}
	if snapshot.blocked {
		return "", "", nativeReject("human/unknown hold blocks native action")
	}
	held := nativePublicationHasLabel(snapshot.pull, policy.HoldLabel)
	switch request.action {
	case "admit":
		if held || len(snapshot.holds) != 0 {
			return "", "", nativeReject("held native PR cannot be admitted")
		}
		if err := p.nativeActionAdmission(ctx, snapshot, intent); err != nil {
			return "", "", fmt.Errorf("verify native admission: %w", err)
		}
		marker := "<!-- " + nativePublicationMarker + "admit:" + request.head + " -->"
		if nativeRobotMarker(snapshot.comments, marker) {
			return "", "already-admitted", nil
		}
		return marker + "\n/ok-to-test", "", nil
	case "recover-retry":
		if snapshot.cancellationPending {
			return "", "", nativeReject("hold cancellation pending")
		}
		body, err := nativeRecoveryComment(snapshot.comments, ci, request.head, request.evidence, held)
		return body, "", err
	case "release-hold":
		if !held || snapshot.cancellationPending {
			return "", "not-held-or-cancel-pending", nil
		}
		if len(snapshot.holds) == 0 || len(ci.Blocked) != 0 || !slices.Contains(ci.Positive, "ci/pipeline-gate") || !slices.Contains(ci.Positive, "ci/prow/e2e-parallel") {
			return "", "", nativeReject("required current-head CI must be green")
		}
		return "/hold cancel", "", nil
	case "close-superseded":
		if err := p.nativeActionClosure(ctx, request, intent); err != nil {
			return "", "", fmt.Errorf("verify merged replacement coverage: %w", err)
		}
		if slices.Contains(nativeInventoryClosureRequests(request.head, snapshot.comments), request.replacement) {
			return "", "close-already-requested", nil
		}
		return fmt.Sprintf("<!-- %sclose:%s:%d -->\nCovered by merged replacement https://github.com/%s/pull/%d.\n\n/close", nativePublicationMarker, request.head, request.replacement, p.repository, request.replacement), "", nil
	}
	return "", "", nativeReject("unsupported native action")
}

// NativeRobotMarker recognizes only exact robot-authored durable markers.
func nativeRobotMarker(comments []automation.Comment, marker string) bool {
	return slices.ContainsFunc(comments, func(comment automation.Comment) bool {
		return comment.Author == nativePublicationRobot && strings.Contains(comment.Body, marker)
	})
}

// NativeRecoveryComment licenses a single evidenced retry on a non-pending current head.
func nativeRecoveryComment(comments []automation.Comment, ci model.CIEvidence, head, evidence string, held bool) (string, error) {
	failed := make(map[string]bool, len(ci.Blocked))
	for _, check := range ci.Blocked {
		if check.URL != "" {
			failed[check.URL] = true
		}
	}
	matchedFailure, comparison := false, false
	for _, link := range nativeRecoveryLink.FindAllString(evidence, -1) {
		link = strings.TrimRight(link, ".;,")
		if failed[link] {
			matchedFailure = true
			continue
		}
		comparison = comparison || nativeRecoveryTrustedLink.MatchString(link)
	}
	if !matchedFailure || !comparison {
		return "", nativeReject("recovery needs current failure and trusted comparison run URLs")
	}
	marker := "<!-- " + nativePublicationMarker + "recovery:" + head + " -->"
	if nativeRobotMarker(comments, marker) {
		return "", nativeReject("single recovery budget for head already consumed")
	}
	for _, check := range ci.Blocked {
		if check.Pending {
			return "", nativeReject("current CI pending")
		}
	}
	for _, check := range ci.Blocked {
		switch check.State {
		case "failure", "error", "timed_out", "cancelled", "action_required", "startup_failure":
		default:
			return "", nativeReject("unknown CI state")
		}
	}
	commands := "/retest-required"
	if held {
		commands = "/hold cancel\n/retest-required"
	}
	return marker + "\n" + evidence + "\n\n" + commands, nil
}
