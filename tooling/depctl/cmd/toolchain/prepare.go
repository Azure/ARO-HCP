// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package toolchain

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"regexp"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/toolchain"
)

var patchInput = regexp.MustCompile(`^1\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var baseInput = regexp.MustCompile(`^[a-f0-9]{40}$`)

type rawPreparation struct{ root, base, target string }
type validatedPreparation struct{ root, base, target string }
type preparation struct {
	root, base, target string
	output             io.Writer
	validateOnly       bool
}

// RegisterPreparation adds the local preparation and validation commands to the toolchain group.
func RegisterPreparation(parent *cobra.Command) {
	for _, name := range []string{"prepare", "validate"} {
		validateOnly := name == "validate"
		raw := rawPreparation{}
		cmd := &cobra.Command{Use: name, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			validated, err := raw.Validate(validateOnly)
			if err != nil {
				return err
			}
			options, err := validated.Complete(cmd.OutOrStdout(), validateOnly)
			if err != nil {
				return err
			}
			return options.Run(cmd.Context())
		}}
		cmd.Flags().StringVar(&raw.root, "root", ".", "disposable candidate checkout")
		cmd.Flags().StringVar(&raw.target, "target", "", "exact target Go patch")
		if !validateOnly {
			cmd.Flags().StringVar(&raw.base, "base", "", "readiness base commit")
		}
		parent.AddCommand(cmd)
	}
}

// Validate rejects invalid checkout, patch and base inputs before opening the repository.
func (r rawPreparation) Validate(validateOnly bool) (validatedPreparation, error) {
	if r.root == "" || !patchInput.MatchString(r.target) || (!validateOnly && !baseInput.MatchString(r.base)) {
		return validatedPreparation{}, fmt.Errorf("provide a checkout root, stable Go patch and, for prepare, a full base SHA")
	}
	return validatedPreparation(r), nil
}

// Complete resolves the checkout and output stream used by the operation.
func (v validatedPreparation) Complete(w io.Writer, validateOnly bool) (preparation, error) {
	root, err := filepath.Abs(v.root)
	if err != nil {
		return preparation{}, fmt.Errorf("resolve preparation checkout: %w", err)
	}
	return preparation{root: root, base: v.base, target: v.target, output: w, validateOnly: validateOnly}, nil
}

// Run prepares a candidate or validates a clean candidate commit without publishing.
func (p preparation) Run(ctx context.Context) error {
	if p.validateOnly {
		return toolchain.ValidateCandidate(ctx, p.root, p.target, p.output)
	}
	return toolchain.PrepareCandidate(ctx, p.root, p.base, p.target, p.output)
}
