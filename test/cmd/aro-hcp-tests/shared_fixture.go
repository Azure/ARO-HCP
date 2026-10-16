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

package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/go-logr/stdr"
	"github.com/spf13/cobra"

	"github.com/openshift-eng/openshift-tests-extension/pkg/dbtime"
	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"

	"github.com/Azure/ARO-HCP/test/e2e"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

type fixtureResources interface {
	Provision(context.Context) (framework.SharedClusterManifest, error)
	Cleanup(context.Context) error
}

type fixtureResourceFactory func(runID, manifestPath string) (fixtureResources, error)

func newFixtureResources(runID, manifestPath string) (fixtureResources, error) {
	return framework.NewRunResourceContext(runID, manifestPath, e2e.TestArtifactsFS,
		stdr.New(log.New(os.Stderr, "[shared-cluster] ", log.LstdFlags)))
}

type sharedFixtureInvocation struct {
	ctx          context.Context
	owner        bool
	runID        string
	manifestPath string
	manifest     framework.SharedClusterManifest
	factory      fixtureResourceFactory
	resources    fixtureResources
	once         sync.Once
	setupErr     error
	started      bool
}

func (invocation *sharedFixtureInvocation) ensureReady() {
	invocation.once.Do(func() {
		invocation.started = true
		if !invocation.owner {
			invocation.manifest, invocation.setupErr = framework.ReadSharedClusterManifest()
			return
		}
		invocation.setupErr = invocation.provision()
	})
}

func (invocation *sharedFixtureInvocation) provision() error {
	base := os.Getenv("ARTIFACT_DIR")
	if base != "" {
		if err := os.MkdirAll(base, 0700); err != nil {
			return err
		}
	}
	directory, err := os.MkdirTemp(base, "shared-read-only-")
	if err != nil {
		return err
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return err
	}
	invocation.manifestPath = filepath.Join(directory, "manifest.json")
	if err := os.Setenv(framework.SharedFixtureManifestEnv, invocation.manifestPath); err != nil {
		return err
	}
	invocation.resources, err = invocation.factory(invocation.runID, invocation.manifestPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(invocation.ctx, 3*time.Hour)
	defer cancel()
	fmt.Fprintf(os.Stderr, "[shared-cluster] provisioning fixture for run %s\n", invocation.runID)
	invocation.manifest, err = invocation.resources.Provision(ctx)
	if err != nil {
		return fmt.Errorf("provision shared read-only cluster: %w", err)
	}
	if err := framework.WriteSharedClusterManifest(invocation.manifestPath, invocation.manifest); err != nil {
		return err
	}
	_, err = framework.ReadSharedClusterManifest()
	return err
}

func (invocation *sharedFixtureInvocation) cleanup() error {
	if !invocation.owner || invocation.resources == nil {
		return nil
	}
	invocation.manifest.Ready = false
	manifestErr := framework.WriteSharedClusterManifest(invocation.manifestPath, invocation.manifest)
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	fmt.Fprintf(os.Stderr, "[shared-cluster] cleaning up fixture for run %s\n", invocation.runID)
	return errors.Join(manifestErr, invocation.resources.Cleanup(ctx))
}

type sharedFixtureRunner struct {
	active  *sharedFixtureInvocation
	factory fixtureResourceFactory
}

func newSharedFixtureRunner(specs et.ExtensionTestSpecs, factory fixtureResourceFactory) *sharedFixtureRunner {
	runner := &sharedFixtureRunner{factory: factory}
	var borrowers et.ExtensionTestSpecs
	for _, spec := range specs {
		if !spec.Labels.Has(labels.RequireSharedReadOnlyCluster[0]) {
			continue
		}
		borrowers = append(borrowers, spec)
		spec.Run = runner.guard(spec.Run)
		if spec.RunParallel != nil {
			spec.RunParallel = runner.guard(spec.RunParallel)
		}
	}
	borrowers.AddBeforeAll(func() {
		if runner.active != nil {
			runner.active.ensureReady()
		}
	})
	return runner
}

func (runner *sharedFixtureRunner) guard(run func(context.Context) *et.ExtensionTestResult) func(context.Context) *et.ExtensionTestResult {
	return func(ctx context.Context) *et.ExtensionTestResult {
		var err error
		if runner.active == nil || !runner.active.started {
			err = fmt.Errorf("shared fixture must be initialized by the run-suite/run-test owner")
		} else {
			err = runner.active.setupErr
		}
		if err != nil {
			now := dbtime.Ptr(time.Now())
			return &et.ExtensionTestResult{Result: et.ResultFailed, Error: err.Error(), StartTime: now, EndTime: now}
		}
		return run(ctx)
	}
}

func (runner *sharedFixtureRunner) wrapRun(command *cobra.Command) {
	execute := command.RunE
	command.RunE = func(command *cobra.Command, args []string) (retErr error) {
		ctx, cancel := signal.NotifyContext(command.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		command.SetContext(ctx)
		invocation := &sharedFixtureInvocation{ctx: ctx, factory: runner.factory}
		runID := os.Getenv(framework.SharedFixtureRunIDEnv)
		ownerText := os.Getenv(framework.SharedFixtureOwnerPIDEnv)
		manifestPath := os.Getenv(framework.SharedFixtureManifestEnv)
		if runID != "" || ownerText != "" || manifestPath != "" {
			ownerPID, err := strconv.Atoi(ownerText)
			if runID == "" || err != nil || ownerPID <= 0 || ownerPID != os.Getppid() {
				return fmt.Errorf("invalid or stale inherited shared-fixture run identity; refusing to provision")
			}
			invocation.runID = runID
		} else {
			invocation.owner = true
			invocation.runID = rand.Text()
			defer func() {
				retErr = errors.Join(retErr, os.Unsetenv(framework.SharedFixtureRunIDEnv), os.Unsetenv(framework.SharedFixtureOwnerPIDEnv), os.Unsetenv(framework.SharedFixtureManifestEnv))
			}()
			if err := os.Setenv(framework.SharedFixtureRunIDEnv, invocation.runID); err != nil {
				return err
			}
			if err := os.Setenv(framework.SharedFixtureOwnerPIDEnv, strconv.Itoa(os.Getpid())); err != nil {
				return err
			}
		}
		runner.active = invocation
		defer func() {
			retErr = errors.Join(retErr, invocation.setupErr, invocation.cleanup())
			runner.active = nil
		}()
		return execute(command, args)
	}
}
