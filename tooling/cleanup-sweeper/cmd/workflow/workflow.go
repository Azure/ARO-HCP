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

// Package workflow defines the contract between the cleanup-sweeper command and
// its cleanup workflows. Workflow-specific settings are declared by each
// workflow itself and supplied as --options=<workflow>.<key>=<value>.
package workflow

import (
	"context"
	"time"

	"github.com/spf13/pflag"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// Common holds the options that apply to every workflow.
type Common struct {
	// DryRun previews what would be deleted without changing anything.
	DryRun bool
	// Parallelism bounds concurrent deletions per step.
	Parallelism int
	// Timeout bounds the whole run; zero means the workflow's own default, if any.
	Timeout time.Duration
	// PolicyFile is the policy document for the selected workflow, for the
	// workflows that take one.
	PolicyFile string
}

// Workflow is one cleanup workflow. A Workflow value holds the state its own
// options are bound to, so every command invocation needs its own instances.
type Workflow interface {
	// Name is the value of --workflow selecting this workflow and the <workflow>
	// prefix of its options.
	Name() string
	// AttachOptions declares the options this workflow accepts through
	// --options=<name>.<key>=<value>. The workflow owns each option's key,
	// default and help; workflows without options declare nothing.
	AttachOptions(flags *pflag.FlagSet)
	// Validate checks the applied options and the common options, including
	// loading the policy file, before any credential is created.
	Validate(common Common) error
	// LogValues returns key/value pairs, such as the target subscription, that
	// identify what the workflow acts on. The command adds them to every log entry.
	LogValues() []any
	// Run executes the workflow.
	Run(ctx context.Context, common Common, credential azcore.TokenCredential) error
}
