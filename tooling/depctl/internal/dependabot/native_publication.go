// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package dependabot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	gh "github.com/google/go-github/v69/github"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/repository"
	automation "github.com/Azure/ARO-HCP/tooling/github"
)

var errNativePublicationBaseMoved = errors.New("main moved; rebuild and validate against the new base")

// NativePublisher publishes authorized native dependency replacements with explicit credentials.
type NativePublisher struct {
	repository, owner, repo string
	api, ci, writer         *gh.Client
	shared                  *automation.Client
}

// NativePublisherOption configures an authenticated publisher dependency without reading credentials.
type NativePublisherOption func(*NativePublisher) error

// WithNativePublicationClients injects separate App readers/writers and the CI evidence reader.
func WithNativePublicationClients(api, ci, writer *gh.Client) NativePublisherOption {
	return func(p *NativePublisher) error {
		if api == nil || ci == nil || writer == nil {
			return fmt.Errorf("native publication requires API, CI and writer clients")
		}
		p.api, p.ci, p.writer = api, ci, writer
		return nil
	}
}

// WithNativeInventoryClients supplies read-only API and CI clients without enabling publication.
func WithNativeInventoryClients(api, ci *gh.Client) NativePublisherOption {
	return func(p *NativePublisher) error {
		if api == nil || ci == nil {
			return fmt.Errorf("native inventory requires API and CI clients")
		}
		p.api, p.ci, p.writer = api, ci, nil
		return nil
	}
}

var nativePublicationRepository = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// NewNativePublisher binds one repository to caller-supplied SDK clients without authentication side effects.
func NewNativePublisher(repository string, options ...NativePublisherOption) (*NativePublisher, error) {
	if !nativePublicationRepository.MatchString(repository) {
		return nil, fmt.Errorf("repository must be OWNER/REPO")
	}
	parts := strings.Split(repository, "/")
	p := &NativePublisher{repository: repository, owner: parts[0], repo: parts[1]}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("nil native publisher option")
		}
		if err := option(p); err != nil {
			return nil, fmt.Errorf("configure native publisher: %w", err)
		}
	}
	if p.api == nil || p.ci == nil {
		return nil, fmt.Errorf("native maintenance requires injected API and CI clients")
	}
	shared, err := automation.NewClient(repository, p.api)
	if err != nil {
		return nil, fmt.Errorf("construct publication reader: %w", err)
	}
	shared, err = shared.WithCIReader(p.ci)
	if err != nil {
		return nil, fmt.Errorf("construct publication CI reader: %w", err)
	}
	p.shared = shared
	if p.writer != nil {
		shared, err = shared.WithWriter(p.writer)
		if err != nil {
			return nil, fmt.Errorf("construct publication writer: %w", err)
		}
		p.shared, err = shared.WithActor(nativePublicationRobot)
		if err != nil {
			return nil, fmt.Errorf("bind publication actor: %w", err)
		}
	}
	return p, nil
}

// NativePublishOptions identifies a validated request and the checkout whose exact origin is trusted.
type NativePublishOptions struct {
	Root    string
	Request *NativePublicationRequest
}

// Publish prepares an isolated Git tree, defers stale candidates before pushing, and publishes through the shared PR library.
func (p *NativePublisher) Publish(ctx context.Context, options NativePublishOptions) (result map[string]any, err error) {
	defer func() {
		if errors.Is(err, errNativePublicationBaseMoved) && !errors.Is(err, automation.ErrIndeterminate) {
			result = map[string]any{"status": "deferred", "reason": err.Error()}
			err = nil
		}
	}()
	if options.Request == nil {
		return map[string]any{"status": "no-request"}, nil
	}
	if p.writer == nil {
		return nil, fmt.Errorf("native publication requires an injected writer client")
	}
	request := options.Request.request
	paths, err := nativeValidatePublicationRequest(request)
	if err != nil {
		return nil, fmt.Errorf("validate publication request: %w", err)
	}
	if options.Root == "" {
		return nil, fmt.Errorf("publication checkout is required")
	}
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return nil, fmt.Errorf("resolve publication checkout: %w", err)
	}
	authorization, err := p.authorize(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("authorize native publication: %w", err)
	}
	marker, err := nativeEncodePublicationMarker(authorization.group["group"], request.SourcePRs)
	if err != nil {
		return nil, err
	}
	title, _ := nativeSafeText(request.Title, true)
	body, _ := nativeSafeText(request.Body, false)
	body += "\n\n" + marker
	source := &repository.Repo{Path: root}
	origin, err := source.NativeGit(ctx, nil, "", "config", "--get", "remote.origin.url")
	if err != nil {
		return nil, fmt.Errorf("read publication origin: %w", err)
	}
	remote := strings.TrimSpace(string(origin))
	if remote != "https://github.com/"+p.repository && remote != "https://github.com/"+p.repository+".git" && remote != "git@github.com:"+p.repository+".git" {
		return nil, fmt.Errorf("publication origin is not the exact repository")
	}
	directory, err := os.MkdirTemp("", "native-dependabot-")
	if err != nil {
		return nil, fmt.Errorf("create isolated publication repository: %w", err)
	}
	defer os.RemoveAll(directory)
	git := nativePublicationGit{repo: &repository.Repo{Path: directory}, remote: "https://github.com/" + p.repository + ".git"}
	if _, err := git.run(ctx, nil, "init", "--quiet", "--template="); err != nil {
		return nil, fmt.Errorf("initialize publication repository: %w", err)
	}
	branch := "native-dependabot-" + authorization.group["group"].(string)
	head, old, previous, unchanged, err := p.preparePublication(ctx, git, request, authorization, paths, marker, title, branch)
	if err != nil {
		return nil, err
	}
	if err := p.ensurePublicationLabel(ctx); err != nil {
		return nil, err
	}
	if err := p.recheckPublication(ctx, request, authorization, old, previous); err != nil {
		return nil, err
	}
	if err := git.publish(ctx, request.ExpectedBaseSHA, branch, old, head, unchanged); err != nil {
		return nil, err
	}
	if err := p.recheckPublication(ctx, request, authorization, head, previous); err != nil {
		return nil, fmt.Errorf("recheck after branch publication; inspect remote before retry: %w: %w", automation.ErrIndeterminate, err)
	}
	previousMarker := ""
	if authorization.existing != nil {
		for _, line := range strings.Split(authorization.existing.GetBody(), "\n") {
			if strings.HasPrefix(line, "<!-- "+nativePublicationMarker) {
				previousMarker = line
			}
		}
	}
	receipt, err := p.shared.Publish(ctx, automation.PublicationRequest{
		ExpectedNumber: authorization.existing.GetNumber(),
		ExpectedTitle:  authorization.existing.GetTitle(), ExpectedBody: authorization.existing.GetBody(),
		HeadRepository: p.repository, HeadRef: branch, ExpectedHead: head,
		BaseRef: "main", ExpectedBase: request.ExpectedBaseSHA,
		Actor: nativePublicationRobot, Marker: marker, PreviousMarker: previousMarker,
		Title: title, Body: body, ExpectedFiles: paths, ReadyForReview: true,
	})
	if err != nil {
		return nil, fmt.Errorf("publish replacement PR: %w", err)
	}
	if authorization.existing == nil || !nativePublicationHasLabel(authorization.existing, nativePublicationLabel) {
		if _, _, err := p.writer.Issues.AddLabelsToIssue(ctx, p.owner, p.repo, receipt.Number, []string{nativePublicationLabel}); err != nil {
			return nil, fmt.Errorf("label replacement; inspect before retry: %w: %w", automation.ErrIndeterminate, err)
		}
	}
	final, err := p.pullDetail(ctx, receipt.Number)
	if err != nil {
		return nil, fmt.Errorf("read published replacement: %w: %w", automation.ErrIndeterminate, err)
	}
	metadata, err := p.validateReplacement(final, false, false)
	expected, decodeErr := nativePublicationMetadata(marker)
	if err != nil || decodeErr != nil || final.GetHead().GetSHA() != head || final.GetTitle() != title || final.GetBody() != body || !reflect.DeepEqual(metadata, expected) {
		return nil, fmt.Errorf("published replacement verification failed: %w", automation.ErrIndeterminate)
	}
	return map[string]any{"number": receipt.Number, "head_sha": head, "group": authorization.group["group"], "reused": authorization.existing != nil, "unchanged": unchanged}, nil
}

// Native marker serialization matches the existing canonical sorted JSON ownership protocol.
func nativeEncodePublicationMarker(group, sources any) (string, error) {
	if typed, ok := sources.([]nativePublicationSource); ok {
		canonical := make([]map[string]any, len(typed))
		for i, source := range typed {
			canonical[i] = map[string]any{"head_sha": source.HeadSHA, "number": source.Number}
		}
		sources = canonical
	}
	var body strings.Builder
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(map[string]any{"group": group, "sources": sources}); err != nil {
		return "", fmt.Errorf("encode native ownership marker: %w", err)
	}
	return "<!-- " + nativePublicationMarker + strings.TrimSuffix(body.String(), "\n") + " -->", nil
}

// Native publication history retains only identities that authorize predecessor reuse.
type nativePublicationHistory struct {
	number                           int
	authorLogin, authorType          string
	state                            string
	merged, draft                    bool
	headRepository, headRef, headSHA string
	baseRepository, baseRef          string
	mergeCommitSHA                   string
	hasMarker, hasLabel              bool
	ownership                        map[string]any
}

// NativePublicationHistoryIdentity excludes mutable repository metadata and unrelated PR prose.
func nativePublicationHistoryIdentity(pull *gh.PullRequest) (nativePublicationHistory, error) {
	identity := nativePublicationHistory{
		number:      pull.GetNumber(),
		authorLogin: pull.GetUser().GetLogin(), authorType: pull.GetUser().GetType(),
		state: pull.GetState(), merged: pull.GetMerged(), draft: pull.GetDraft(),
		headRepository: pull.GetHead().GetRepo().GetFullName(), headRef: pull.GetHead().GetRef(), headSHA: pull.GetHead().GetSHA(),
		baseRepository: pull.GetBase().GetRepo().GetFullName(), baseRef: pull.GetBase().GetRef(),
		mergeCommitSHA: pull.GetMergeCommitSHA(),
		hasMarker:      strings.Contains(pull.GetBody(), nativePublicationMarker),
		hasLabel:       nativePublicationHasLabel(pull, nativePublicationLabel),
	}
	ownership, err := nativePublicationMetadata(pull.GetBody())
	if err != nil {
		// Closed-unmerged entries block revival structurally, even without a valid ownership marker.
		if !pull.GetMerged() {
			return identity, nil
		}
		return identity, fmt.Errorf("read replacement #%d history ownership: %w", pull.GetNumber(), err)
	}
	identity.ownership = ownership
	return identity, nil
}

// NativePublicationHistoryInventory compares complete histories by PR number, independent of API ordering.
func nativePublicationHistoryInventory(merged, closed []*gh.PullRequest) (map[int]nativePublicationHistory, error) {
	if len(merged)+len(closed) == 0 {
		return nil, nil
	}
	history := make(map[int]nativePublicationHistory, len(merged)+len(closed))
	for _, inventory := range [][]*gh.PullRequest{merged, closed} {
		for _, pull := range inventory {
			identity, err := nativePublicationHistoryIdentity(pull)
			if err != nil {
				return nil, fmt.Errorf("read replacement history inventory: %w", err)
			}
			history[pull.GetNumber()] = identity
		}
	}
	return history, nil
}

// RecheckPublication repeats complete source/replacement inventory and hold authorization at each write boundary.
func (p *NativePublisher) recheckPublication(ctx context.Context, request nativePublicationRequest, original nativePublicationAuthorization, head string, previous *gh.PullRequest) error {
	fresh, err := p.authorize(ctx, request)
	if err != nil {
		return fmt.Errorf("recheck publication authorization: %w", err)
	}
	if !reflect.DeepEqual(fresh.group, original.group) {
		return fmt.Errorf("source intent changed before publication")
	}
	if (fresh.existing == nil) != (original.existing == nil) {
		return fmt.Errorf("replacement inventory changed before publication")
	}
	if original.existing != nil && (fresh.existing.GetNumber() != original.existing.GetNumber() || fresh.existing.GetHead().GetSHA() != head || fresh.existing.GetBody() != original.existing.GetBody() || fresh.existing.GetTitle() != original.existing.GetTitle()) {
		return fmt.Errorf("replacement authorization changed before publication")
	}
	freshHistory, err := nativePublicationHistoryInventory(fresh.merged, fresh.closed)
	if err != nil {
		return fmt.Errorf("recheck replacement history: %w", err)
	}
	originalHistory, err := nativePublicationHistoryInventory(original.merged, original.closed)
	if err != nil {
		return fmt.Errorf("read original replacement history: %w", err)
	}
	if !reflect.DeepEqual(freshHistory, originalHistory) {
		return fmt.Errorf("replacement history changed before publication")
	}
	if previous != nil {
		detail, err := p.pullDetail(ctx, previous.GetNumber())
		if err != nil {
			return fmt.Errorf("reread merged predecessor: %w", err)
		}
		currentIdentity, err := nativePublicationHistoryIdentity(detail)
		if err != nil {
			return fmt.Errorf("reread merged predecessor identity: %w", err)
		}
		previousIdentity, err := nativePublicationHistoryIdentity(previous)
		if err != nil {
			return fmt.Errorf("read original merged predecessor identity: %w", err)
		}
		if !reflect.DeepEqual(currentIdentity, previousIdentity) {
			return fmt.Errorf("merged predecessor authorization changed")
		}
		if _, err := p.validateReplacement(detail, true, false); err != nil {
			return fmt.Errorf("revalidate merged predecessor: %w", err)
		}
	}
	return nil
}

// EnsurePublicationLabel creates only the fixed label and verifies its immutable positive identity.
func (p *NativePublisher) ensurePublicationLabel(ctx context.Context) error {
	label, _, err := p.api.Issues.GetLabel(ctx, p.owner, p.repo, nativePublicationLabel)
	var response *gh.ErrorResponse
	if errors.As(err, &response) && response.Response != nil && response.Response.StatusCode == http.StatusNotFound {
		_, _, createErr := p.writer.Issues.CreateLabel(ctx, p.owner, p.repo, &gh.Label{Name: gh.Ptr(nativePublicationLabel), Color: gh.Ptr("0366d6"), Description: gh.Ptr("Validated native Dependabot replacement maintained by gh-aw-recompile")})
		response = nil
		if createErr != nil && (!errors.As(createErr, &response) || response.Response == nil || response.Response.StatusCode != http.StatusUnprocessableEntity) {
			return fmt.Errorf("create fixed label; inspect before retry: %w: %w", automation.ErrIndeterminate, createErr)
		}
		label, _, err = p.api.Issues.GetLabel(ctx, p.owner, p.repo, nativePublicationLabel)
	}
	if err != nil {
		return fmt.Errorf("read fixed publication label: %w", err)
	}
	if label == nil || label.GetName() != nativePublicationLabel || label.GetID() <= 0 {
		return fmt.Errorf("fixed dependency-upgrade label could not be verified")
	}
	return nil
}
