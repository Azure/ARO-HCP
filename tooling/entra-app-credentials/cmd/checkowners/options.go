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

package checkowners

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/cmd/internal/common"
	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/owners"
)

const defaultTimeout = 2 * time.Minute

// RawOptions contains the check-owners command flags before validation.
type RawOptions struct {
	GroupID  string
	OwnerIDs string
	Timeout  time.Duration
}

// ValidatedOptions contains normalized inputs.
type ValidatedOptions struct {
	groupID  string
	ownerIDs []string
	timeout  time.Duration
}

// Options contains completed runtime dependencies.
type Options struct {
	*ValidatedOptions
	directory owners.Directory
	// report receives the drift report, which is multi-line and unreadable
	// once escaped into the structured error log.
	report io.Writer
}

// DefaultOptions returns the default check-owners command options.
func DefaultOptions() *RawOptions {
	return &RawOptions{Timeout: defaultTimeout}
}

// BindOptions binds command-line flags to raw options.
func BindOptions(opts *RawOptions, cmd *cobra.Command) {
	cmd.Flags().StringVar(&opts.GroupID, "group-id", opts.GroupID, "Object ID of the Entra group whose members must be the application owners.")
	cmd.Flags().StringVar(&opts.OwnerIDs, "owner-ids", opts.OwnerIDs, "Comma-separated configured owner object IDs (entraAppOwnerIds).")
	cmd.Flags().DurationVar(&opts.Timeout, "timeout", opts.Timeout, "Timeout for the whole check.")
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	groupID := strings.TrimSpace(o.GroupID)
	if _, err := uuid.Parse(groupID); err != nil {
		return nil, fmt.Errorf("--group-id must be an Entra group object ID")
	}
	if o.Timeout <= 0 {
		return nil, fmt.Errorf("--timeout must be greater than zero")
	}
	ownerIDs, err := owners.ParseIDs(o.OwnerIDs)
	if err != nil {
		return nil, fmt.Errorf("--owner-ids: %w", err)
	}
	return &ValidatedOptions{groupID: strings.ToLower(groupID), ownerIDs: ownerIDs, timeout: o.Timeout}, nil
}

func (o *ValidatedOptions) Complete(_ context.Context) (*Options, error) {
	credential, err := common.NewCredential()
	if err != nil {
		return nil, err
	}
	directory, err := owners.NewGraphDirectory(credential)
	if err != nil {
		return nil, err
	}
	return &Options{ValidatedOptions: o, directory: directory, report: os.Stderr}, nil
}

func (o *Options) Run(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	err := owners.Check(ctx, o.directory, o.groupID, o.ownerIDs)

	var drift *owners.Drift
	if errors.As(err, &drift) {
		fmt.Fprintln(o.report, drift.Error())
		return errors.New("configured owner IDs differ from the group members; update entraAppOwnerIds as listed above")
	}
	return err
}
