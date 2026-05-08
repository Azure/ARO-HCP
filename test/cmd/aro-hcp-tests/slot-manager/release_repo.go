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

package slotmanager

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

func BindReleaseRepoOptions(opts *RawReleaseRepoOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.ReleaseRepo, "release-repo", opts.ReleaseRepo, "Path to the openshift/release checkout")
	cmd.Flags().StringVar(&opts.CatalogPath, "slot-catalog", opts.CatalogPath, "Path to the canonical E2E slot catalog")
	if err := cmd.MarkFlagRequired("release-repo"); err != nil {
		return fmt.Errorf("failed to mark flag %q as required: %w", "release-repo", err)
	}
	return nil
}

type RawReleaseRepoOptions struct {
	ReleaseRepo string
	CatalogPath string
}

type validatedReleaseRepoOptions struct {
	*RawReleaseRepoOptions
}

type ValidatedReleaseRepoOptions struct {
	*validatedReleaseRepoOptions
}

type completedReleaseRepoOptions struct {
	ReleaseRepo string
	Catalog     *slots.Catalog
}

type ReleaseRepoOptions struct {
	*completedReleaseRepoOptions
}

func (o *RawReleaseRepoOptions) Validate() (*ValidatedReleaseRepoOptions, error) {
	if strings.TrimSpace(o.ReleaseRepo) == "" {
		return nil, fmt.Errorf("--release-repo must not be empty")
	}
	return &ValidatedReleaseRepoOptions{
		validatedReleaseRepoOptions: &validatedReleaseRepoOptions{RawReleaseRepoOptions: o},
	}, nil
}

func (o *ValidatedReleaseRepoOptions) Complete(_ context.Context) (*ReleaseRepoOptions, error) {
	catalog, err := slots.LoadCatalog(o.CatalogPath)
	if err != nil {
		return nil, err
	}
	return &ReleaseRepoOptions{
		completedReleaseRepoOptions: &completedReleaseRepoOptions{
			ReleaseRepo: o.ReleaseRepo,
			Catalog:     catalog,
		},
	}, nil
}

func newSyncBoskosConfigCommand() (*cobra.Command, error) {
	opts := &RawReleaseRepoOptions{}

	cmd := &cobra.Command{
		Use:   "sync-boskos-config",
		Short: "Rewrite the ARO-HCP managed Boskos section in a release checkout.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return SyncBoskosConfig(cmd.Context(), opts)
		},
	}

	if err := BindReleaseRepoOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

func SyncBoskosConfig(ctx context.Context, opts *RawReleaseRepoOptions) error {
	validated, err := opts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}
	return completed.SyncRun(ctx)
}

func (o *ReleaseRepoOptions) SyncRun(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)
	if err := slots.RewriteGenerateBoskos(o.ReleaseRepo, o.Catalog); err != nil {
		return err
	}
	logger.Info("Updated release Boskos generator from the slot catalog", "path", slots.GenerateBoskosPythonPath(o.ReleaseRepo))
	return nil
}

func newValidateBoskosConfigCommand() (*cobra.Command, error) {
	opts := &RawReleaseRepoOptions{}

	cmd := &cobra.Command{
		Use:   "validate-boskos-config",
		Short: "Validate that a release checkout Boskos config matches the slot catalog.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return ValidateBoskosConfig(cmd.Context(), opts)
		},
	}

	if err := BindReleaseRepoOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

func ValidateBoskosConfig(ctx context.Context, opts *RawReleaseRepoOptions) error {
	validated, err := opts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}
	return completed.ValidateRun(ctx)
}

func (o *ReleaseRepoOptions) ValidateRun(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)
	if err := slots.ValidateBoskosConfig(o.ReleaseRepo, o.Catalog); err != nil {
		return err
	}
	logger.Info("Validated Boskos config against the slot catalog", "path", filepath.Base(slots.BoskosYAMLPath(o.ReleaseRepo)))
	return nil
}
