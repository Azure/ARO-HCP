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

package pipeline

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
)

// Commit subjects for the render stage. They match the messages the bash
// script produced so the bump branch keeps the same commit history.
const (
	// ACMCommitMessage covers the ACM helm charts, yaml formatting and
	// helm fixtures, which are regenerated together.
	ACMCommitMessage = "chore: render ACM helm-charts"
	// MaterializeCommitMessage covers the rendered configuration.
	//
	// The script left this change uncommitted and relied on prcreator to
	// commit it while pushing, under the message passed as -git-message.
	// Owning it here keeps every commit in one place; the only visible
	// difference is that prcreator prefixed the PR title onto the subject,
	// which carried no information the title did not already give.
	MaterializeCommitMessage = "chore: render digests using materialize"
)

// makeTarget is one `make` invocation in the render sequence.
type makeTarget struct {
	// directory is the -C argument, empty for the repository root.
	directory string
	// target is the make target to run.
	target string
}

// args renders the target as arguments to make.
func (t makeTarget) args() []string {
	if t.directory == "" {
		return []string{t.target}
	}
	return []string{"-C", t.directory, t.target}
}

// String describes the invocation for logs and errors.
func (t makeTarget) String() string {
	if t.directory == "" {
		return "make " + t.target
	}
	return fmt.Sprintf("make -C %s %s", t.directory, t.target)
}

// acmTargets regenerate the ACM helm charts and everything derived from
// them. They are grouped because a single commit covers their combined
// output.
var acmTargets = []makeTarget{
	{directory: "acm", target: "helm-charts"},
	{target: "yamlfmt"},
	{target: "update-helm-fixtures"},
}

// materializeTarget re-renders the configuration under config/rendered,
// which has to happen after the digest change and the ACM render.
var materializeTarget = makeTarget{directory: "config", target: "materialize"}

// RequiredTools are the executables the render stage shells out to, directly
// or through the make targets it runs. Checking them up front turns a
// mid-run failure deep inside a makefile into one clear message, which is
// what the script's preflight loop was for.
var RequiredTools = []string{"git", "make", "az", "bicep", "yq", "jq", "curl"}

// render runs the post-update render sequence: regenerate the ACM helm
// charts, commit them if they changed, then materialize the configuration.
//
// Two commits rather than one because the ACM output is frequently
// unchanged, and an empty commit would be noise in the bump PR.
func (p *Pipeline) render(ctx context.Context, result *Result) error {
	logger := logr.FromContextOrDiscard(ctx)

	logger.Info("rendering ACM helm-charts")
	for _, target := range acmTargets {
		if err := p.runMake(ctx, target); err != nil {
			return err
		}
	}

	if p.opts.Commit {
		if err := p.commit(ctx, result, ACMCommitMessage); err != nil {
			return err
		}
	}

	logger.Info("materializing configuration")
	if err := p.runMake(ctx, materializeTarget); err != nil {
		return err
	}

	if p.opts.Commit {
		if err := p.commit(ctx, result, MaterializeCommitMessage); err != nil {
			return err
		}
	}

	return nil
}

// runMake invokes a make target at the repository root.
func (p *Pipeline) runMake(ctx context.Context, target makeTarget) error {
	logr.FromContextOrDiscard(ctx).V(1).Info("running make target", "target", target.String())
	if err := p.repo.Runner().Run(ctx, "make", target.args()...); err != nil {
		return fmt.Errorf("%s failed: %w", target, err)
	}
	return nil
}
