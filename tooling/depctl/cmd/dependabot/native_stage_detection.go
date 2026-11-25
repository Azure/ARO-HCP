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

package dependabot

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/dependabot"
)

type nativeStageDetectionRawOptions struct {
	repository, outputFile, detectionDir string
}

type nativeStageDetectionValidatedOptions struct {
	outputFile, detectionDir string
}

type nativeStageDetectionOptions struct {
	outputFile, detectionDir string
	output                   *json.Encoder
}

// Builds the offline native detection staging command.
func newNativeStageDetectionCommand() *cobra.Command {
	var options nativeStageDetectionRawOptions
	command := &cobra.Command{
		Use:   "stage-detection",
		Short: "Stage a verified native replacement for threat detection without network access",
		Args:  cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			validated, err := options.Validate()
			if err != nil {
				return cli.Invalid(err)
			}
			completed := validated.Complete(cmd.OutOrStdout())
			return completed.Run()
		},
	}
	command.Flags().StringVar(&options.repository, "repository", "", "OWNER/REPO")
	command.Flags().StringVar(&options.outputFile, "output-file", "", "original agent output JSON file")
	command.Flags().StringVar(&options.detectionDir, "detection-dir", "", "prepared threat detection directory")
	return command
}

// Validate checks the repository identity and requires both staging paths before any file access.
func (o nativeStageDetectionRawOptions) Validate() (nativeStageDetectionValidatedOptions, error) {
	if !nativeRequestRepositoryPattern.MatchString(o.repository) {
		return nativeStageDetectionValidatedOptions{}, errors.New("repository must be OWNER/REPO")
	}
	if o.outputFile == "" || o.detectionDir == "" {
		return nativeStageDetectionValidatedOptions{}, errors.New("--output-file and --detection-dir are required")
	}
	return nativeStageDetectionValidatedOptions{outputFile: o.outputFile, detectionDir: o.detectionDir}, nil
}

// Complete binds the JSON encoder without accessing staging files or rewriting their paths.
func (o nativeStageDetectionValidatedOptions) Complete(output io.Writer) nativeStageDetectionOptions {
	return nativeStageDetectionOptions{
		outputFile:   o.outputFile,
		detectionDir: o.detectionDir,
		output:       json.NewEncoder(output),
	}
}

// Run stages the detector copy and emits the result as JSON.
func (o nativeStageDetectionOptions) Run() error {
	result, err := dependabot.StageNativeDetection(o.outputFile, o.detectionDir)
	if err != nil {
		return fmt.Errorf("stage native detection: %w", err)
	}
	if err := o.output.Encode(result); err != nil {
		return fmt.Errorf("encode native detection result: %w", err)
	}
	return nil
}
