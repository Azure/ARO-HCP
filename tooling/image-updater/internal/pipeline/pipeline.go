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

// Package pipeline sequences the stages of an automated image bump: update
// digests, commit, render, open a pull request, notify. It is the Go
// replacement for the Prow step-registry bash script, and each stage is
// opt-in via a flag so a developer running the tool by hand gets only the
// digest update.
package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/command"
	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/git"
)

// DigestCommitMessage is the subject of the commit holding the digest
// changes. It matches the message the bash script used, so the commit history
// of the bump branch does not change shape with this port.
const DigestCommitMessage = "chore: execute image-updater for all components"

// summaryFormat is the format used for the run summary. It is fixed at
// markdown independently of --output-format, because the summary is destined
// for a pull request body.
const summaryFormat = "markdown"

// Outcome classifies how a run ended.
//
// This is a typed signal rather than an exit code because AROSLSRE-1735
// (PagerDuty alerting) needs to distinguish a healthy run that found nothing
// from a run that actually did work, and from a failure.
type Outcome string

const (
	// OutcomeNoChanges means no component image had a new digest. This is a
	// successful run, not a failure.
	OutcomeNoChanges Outcome = "no-changes"
	// OutcomeUpdated means digests changed on disk but were not committed,
	// because --commit was not requested.
	OutcomeUpdated Outcome = "updated"
	// OutcomeCommitted means the changes were committed locally.
	OutcomeCommitted Outcome = "committed"
)

// Result describes what a run did.
type Result struct {
	// Outcome classifies the run.
	Outcome Outcome
	// Summary is the markdown table of digest updates, empty when nothing
	// changed.
	Summary string
	// Commits holds the subjects of the commits made, in order.
	Commits []string
}

// Options configures which stages run and how.
type Options struct {
	// Commit stages and commits the changes each stage produces.
	Commit bool
	// GitUser and GitEmail are the commit identity. Only used with Commit.
	GitUser  string
	GitEmail string
	// Materialize regenerates the ACM helm charts and the rendered
	// configuration after the digest update.
	Materialize bool
}

// ImageUpdater fetches the latest digests and records what changed.
//
// The pipeline depends on this narrow interface rather than the concrete
// updater so that the stages around it -- commit, render, pull request --
// can be tested without reaching a container registry.
type ImageUpdater interface {
	// UpdateImages writes the latest digests to the target files.
	UpdateImages(ctx context.Context) error
	// Summary renders what changed in the given output format.
	Summary(format string) (string, error)
}

// Pipeline runs the bump stages against a working tree.
type Pipeline struct {
	updater ImageUpdater
	repo    *git.Repo
	opts    Options
}

// New returns a Pipeline that updates digests with the given updater and
// commits into the given repository.
func New(u ImageUpdater, repo *git.Repo, opts Options) *Pipeline {
	return &Pipeline{updater: u, repo: repo, opts: opts}
}

// Run executes the requested stages in order and reports what happened.
func (p *Pipeline) Run(ctx context.Context) (*Result, error) {
	logger := logr.FromContextOrDiscard(ctx)

	if err := p.preflight(); err != nil {
		return nil, err
	}

	// Configure the identity before anything can commit, so a
	// misconfiguration fails fast rather than after the registry round
	// trips.
	if p.opts.Commit {
		if err := p.repo.ConfigureIdentity(ctx, p.opts.GitUser, p.opts.GitEmail); err != nil {
			return nil, err
		}
	}

	logger.Info("fetching the latest image digests for all components")
	if err := p.updater.UpdateImages(ctx); err != nil {
		return nil, fmt.Errorf("failed to update image digests: %w", err)
	}

	// The working tree is the source of truth for "did anything change",
	// matching the script. It also covers changes a later stage makes that
	// the updater itself knows nothing about.
	dirty, err := p.repo.IsDirty(ctx)
	if err != nil {
		return nil, err
	}
	if !dirty {
		logger.Info("no new digests found for any component images")
		return &Result{Outcome: OutcomeNoChanges}, nil
	}

	summary, err := p.summary()
	if err != nil {
		return nil, err
	}
	logger.Info("new digests found for some component images")

	result := &Result{Outcome: OutcomeUpdated, Summary: summary}

	if p.opts.Commit {
		if err := p.commit(ctx, result, DigestCommitMessage); err != nil {
			return nil, err
		}
		result.Outcome = OutcomeCommitted
	}

	// Render after committing the digests, so the rendered output is a
	// separate commit from the change that caused it.
	if p.opts.Materialize {
		if err := p.render(ctx, result); err != nil {
			return nil, err
		}
	}

	return result, nil
}

// preflight checks that the executables the requested stages depend on are
// present, so a missing tool fails immediately with one clear message
// instead of surfacing from deep inside a makefile several minutes in.
//
// Unlike the bash script, which demanded every tool on every run, each stage
// declares only what it needs: committing does not require bicep.
func (p *Pipeline) preflight() error {
	required := []string{"git"}
	if p.opts.Materialize {
		required = append(required, RequiredTools...)
	}

	if missing := command.MissingTools(required...); len(missing) > 0 {
		return fmt.Errorf("required tools not found on PATH: %s", strings.Join(missing, ", "))
	}
	return nil
}

// commit records a commit on the result when one was actually made.
func (p *Pipeline) commit(ctx context.Context, result *Result, message string) error {
	committed, err := p.repo.CommitAll(ctx, message)
	if err != nil {
		return err
	}
	if !committed {
		return nil
	}
	logr.FromContextOrDiscard(ctx).Info("committed changes", "message", message)
	result.Commits = append(result.Commits, message)
	return nil
}

// summary renders the digest updates as a markdown table for the pull
// request body.
func (p *Pipeline) summary() (string, error) {
	summary, err := p.updater.Summary(summaryFormat)
	if err != nil {
		return "", fmt.Errorf("failed to format update summary: %w", err)
	}
	return summary, nil
}
