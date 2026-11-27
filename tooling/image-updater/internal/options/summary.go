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

package options

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/output"
	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/summary"
)

// RawSummaryOptions holds the summary command input.
type RawSummaryOptions struct {
	UpdatesPath    string
	ReceiptPath    string
	OutputPath     string
	JSONPath       string
	BodyPath       string
	Revision       string
	BaseCommit     string
	Branch         string
	PullRequestURL string
	JobURL         string
	Prebuilt       bool
}

// receiptFile mirrors the publish receipt fields the summary reads.
type receiptFile struct {
	Number  int
	URL     string
	HeadSHA string
}

// DefaultSummaryOptions returns defaults for the summary command.
func DefaultSummaryOptions() *RawSummaryOptions {
	return &RawSummaryOptions{}
}

// BindSummaryOptions binds the summary flags to the raw options.
func BindSummaryOptions(opts *RawSummaryOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.UpdatesPath, "updates", "", "Path to the update results in JSON format")
	cmd.Flags().StringVar(&opts.ReceiptPath, "receipt", "", "Path to the publish receipt in JSON format")
	cmd.Flags().StringVar(&opts.OutputPath, "output", "", "Path to write the HTML summary")
	cmd.Flags().StringVar(&opts.JSONPath, "json-output", "", "Path to write the JSON summary")
	cmd.Flags().StringVar(&opts.BodyPath, "body-output", "", "Path to write the pull request body in markdown")
	cmd.Flags().StringVar(&opts.Revision, "revision", "", "Revision the run checked out")
	cmd.Flags().StringVar(&opts.BaseCommit, "base-commit", "", "Commit the run prepared its changes from")
	cmd.Flags().StringVar(&opts.Branch, "branch", "", "Branch the run published to")
	cmd.Flags().StringVar(&opts.PullRequestURL, "pull-request-url", "", "Pull request URL to show when no publish receipt exists yet")
	cmd.Flags().StringVar(&opts.JobURL, "job-url", "", "Prow job URL to link from the summary")
	cmd.Flags().BoolVar(&opts.Prebuilt, "prebuilt", false, "Whether the run used the prebuilt templatize")

	for _, name := range []string{"updates", "revision"} {
		if err := cmd.MarkFlagRequired(name); err != nil {
			return err
		}
	}

	return nil
}

// validatedSummaryOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedSummaryOptions struct {
	*RawSummaryOptions
}

// ValidatedSummaryOptions contains validated summary inputs.
type ValidatedSummaryOptions struct {
	*validatedSummaryOptions
}

// Validate checks the input before any artifact is read.
func (o *RawSummaryOptions) Validate(_ context.Context) (*ValidatedSummaryOptions, error) {
	for _, flag := range []struct {
		name  string
		value string
	}{
		{"--updates", o.UpdatesPath},
		{"--revision", o.Revision},
	} {
		if strings.TrimSpace(flag.value) == "" {
			return nil, fmt.Errorf("%s is required", flag.name)
		}
	}
	if strings.TrimSpace(o.OutputPath) == "" && strings.TrimSpace(o.JSONPath) == "" && strings.TrimSpace(o.BodyPath) == "" {
		return nil, fmt.Errorf("one of --output, --json-output or --body-output is required")
	}

	return &ValidatedSummaryOptions{validatedSummaryOptions: &validatedSummaryOptions{RawSummaryOptions: o}}, nil
}

// completedSummaryOptions is a private wrapper that enforces a call of Complete() before the report can be written.
type completedSummaryOptions struct {
	Report  summary.Report
	Output  string
	JSONOut string
	BodyOut string
}

// CompletedSummaryOptions contains the parsed run evidence and its destinations.
type CompletedSummaryOptions struct {
	*completedSummaryOptions
}

// Complete reads the run artifacts into a summary report.
func (v *ValidatedSummaryOptions) Complete(_ context.Context) (*CompletedSummaryOptions, error) {
	updates, err := readUpdates(v.UpdatesPath)
	if err != nil {
		return nil, err
	}
	pullRequest, err := readPullRequest(v.ReceiptPath, v.PullRequestURL)
	if err != nil {
		return nil, err
	}

	templatizeSource := "built from source"
	if v.Prebuilt {
		templatizeSource = "prebuilt from the image"
	}
	updated := 0
	for _, update := range updates {
		if update.Status != "unchanged" {
			updated++
		}
	}

	return &CompletedSummaryOptions{completedSummaryOptions: &completedSummaryOptions{
		Report: summary.Report{
			Revision:          v.Revision,
			BaseCommit:        v.BaseCommit,
			Branch:            v.Branch,
			TemplatizeSource:  templatizeSource,
			GeneratedAt:       time.Now().UTC().Format(time.RFC3339),
			JobURL:            v.JobURL,
			Updates:           updates,
			PullRequest:       pullRequest,
			UpdatedComponents: updated,
		},
		Output:  v.OutputPath,
		JSONOut: v.JSONPath,
		BodyOut: v.BodyPath,
	}}, nil
}

// Run writes the HTML and optional JSON summaries.
func (c *CompletedSummaryOptions) Run() error {
	if c.Output != "" {
		var page bytes.Buffer
		if err := summary.RenderHTML(&page, c.Report); err != nil {
			return err
		}
		if err := writeArtifact(c.Output, page.Bytes()); err != nil {
			return fmt.Errorf("write the HTML summary: %w", err)
		}
	}

	if c.BodyOut != "" {
		var body bytes.Buffer
		if err := summary.RenderMarkdown(&body, c.Report); err != nil {
			return err
		}
		if err := writeArtifact(c.BodyOut, body.Bytes()); err != nil {
			return fmt.Errorf("write the pull request body: %w", err)
		}
	}

	if c.JSONOut == "" {
		return nil
	}
	var encoded bytes.Buffer
	if err := summary.EncodeJSON(&encoded, c.Report); err != nil {
		return err
	}
	if err := writeArtifact(c.JSONOut, encoded.Bytes()); err != nil {
		return fmt.Errorf("write the JSON summary: %w", err)
	}

	return nil
}

// readUpdates parses the update results file.
func readUpdates(path string) ([]summary.Update, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the update results %s: %w", path, err)
	}

	var results []output.UpdateResult
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, fmt.Errorf("parse the update results %s: %w", path, err)
	}

	updates := make([]summary.Update, 0, len(results))
	for _, result := range results {
		updates = append(updates, summary.Update{
			Name:      result.Name,
			OldDigest: result.OldDigest,
			NewDigest: result.NewDigest,
			Tag:       result.Tag,
			Date:      result.Date,
			Status:    result.Status,
		})
	}
	return updates, nil
}

// readPullRequest reads the published pull request from the receipt, or falls
// back to an explicit URL before the run has published anything.
func readPullRequest(receiptPath, url string) (summary.PullRequest, error) {
	if receiptPath == "" {
		return pullRequestFromURL(url), nil
	}

	data, err := os.ReadFile(receiptPath)
	if err != nil {
		return summary.PullRequest{}, fmt.Errorf("read the publish receipt %s: %w", receiptPath, err)
	}

	var receipt receiptFile
	if err := json.Unmarshal(data, &receipt); err != nil {
		return summary.PullRequest{}, fmt.Errorf("parse the publish receipt %s: %w", receiptPath, err)
	}
	if receipt.URL == "" {
		return summary.PullRequest{}, fmt.Errorf("publish receipt %s has no pull request URL", receiptPath)
	}

	return summary.PullRequest{Number: receipt.Number, URL: receipt.URL, HeadSHA: receipt.HeadSHA}, nil
}

// pullRequestFromURL builds the pull request identity from a URL.
func pullRequestFromURL(url string) summary.PullRequest {
	if url == "" {
		return summary.PullRequest{}
	}
	number := 0
	if _, err := fmt.Sscanf(url[strings.LastIndex(url, "/")+1:], "%d", &number); err != nil {
		number = 0
	}
	return summary.PullRequest{Number: number, URL: url}
}

// writeArtifact replaces path atomically, so a failed write leaves the
// previous artifact intact.
func writeArtifact(path string, content []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create the artifact directory: %w", err)
	}

	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create the temporary artifact: %w", err)
	}
	defer os.Remove(temporary.Name())

	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temporary.Name(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}

	return nil
}
