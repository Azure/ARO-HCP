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
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

func DefaultReleaseOptions() *RawReleaseOptions {
	return &RawReleaseOptions{
		SharedDir:           os.Getenv("SHARED_DIR"),
		LeaseProxyServerURL: os.Getenv("LEASE_PROXY_SERVER_URL"),
		LeaseProxyTimeout:   slots.DefaultLeaseProxyTimeout,
	}
}

func BindReleaseOptions(opts *RawReleaseOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.SharedDir, "shared-dir", opts.SharedDir, "Path to SHARED_DIR")
	cmd.Flags().StringVar(&opts.LeaseProxyServerURL, "lease-proxy-server-url", opts.LeaseProxyServerURL, "Lease proxy server URL")
	cmd.Flags().DurationVar(&opts.LeaseProxyTimeout, "lease-proxy-timeout", opts.LeaseProxyTimeout, "Maximum time to spend retrying a lease proxy request before failing.")
	return nil
}

type RawReleaseOptions struct {
	SharedDir           string
	LeaseProxyServerURL string
	LeaseProxyTimeout   time.Duration
}

type validatedReleaseOptions struct {
	*RawReleaseOptions
}

type ValidatedReleaseOptions struct {
	*validatedReleaseOptions
}

type completedReleaseOptions struct {
	SharedDir         string
	LeaseProxyURL     string
	LeaseProxyTimeout time.Duration
}

type ReleaseOptions struct {
	*completedReleaseOptions
}

func newReleaseCommand() (*cobra.Command, error) {
	opts := DefaultReleaseOptions()

	cmd := &cobra.Command{
		Use:   "release",
		Short: "Release the previously acquired ARO-HCP E2E slot lease.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return Release(cmd.Context(), opts)
		},
	}

	if err := BindReleaseOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

func Release(ctx context.Context, opts *RawReleaseOptions) error {
	validated, err := opts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}
	return completed.Run(ctx)
}

func (o *RawReleaseOptions) Validate() (*ValidatedReleaseOptions, error) {
	switch {
	case strings.TrimSpace(o.SharedDir) == "":
		return nil, fmt.Errorf("--shared-dir must not be empty")
	case strings.TrimSpace(o.LeaseProxyServerURL) == "":
		return nil, fmt.Errorf("--lease-proxy-server-url must not be empty")
	case o.LeaseProxyTimeout <= 0:
		return nil, fmt.Errorf("--lease-proxy-timeout must be greater than zero")
	}

	return &ValidatedReleaseOptions{
		validatedReleaseOptions: &validatedReleaseOptions{RawReleaseOptions: o},
	}, nil
}

func (o *ValidatedReleaseOptions) Complete(_ context.Context) (*ReleaseOptions, error) {
	return &ReleaseOptions{
		completedReleaseOptions: &completedReleaseOptions{
			SharedDir:         o.SharedDir,
			LeaseProxyURL:     o.LeaseProxyServerURL,
			LeaseProxyTimeout: o.LeaseProxyTimeout,
		},
	}, nil
}

func (o *ReleaseOptions) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)
	state, err := slots.LoadAcquiredSlotState(o.SharedDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			logger.Info("No acquired slot state found, nothing to release", "sharedDir", o.SharedDir)
			return nil
		}
		return err
	}

	if err := slots.ReleaseLease(ctx, o.LeaseProxyURL, state.LeasedResourceName, o.LeaseProxyTimeout); err != nil {
		return err
	}

	if err := slots.RemoveStateFiles(o.SharedDir); err != nil {
		logger.Error(err, "Failed to remove local slot state after lease release", "sharedDir", o.SharedDir)
	}

	logger.Info("Released slot", "slotName", state.Slot.ResourceName, "sharedDir", o.SharedDir)
	return nil
}
