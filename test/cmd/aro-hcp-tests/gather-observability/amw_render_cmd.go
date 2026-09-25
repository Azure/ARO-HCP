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

package gatherobservability

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// Saved reports include SDK-reencoded responses and indentation, so allow more
// space than the collector's raw response budget.
const amwMaxInputBytes = 64 << 20

func newRenderAMWCommand() *cobra.Command {
	var input, output string
	cmd := &cobra.Command{
		Use:   "render-amw --input amw.json --output observability-summary.html",
		Short: "Render a saved AMW report without Azure configuration or credentials.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(input) == "" || strings.TrimSpace(output) == "" {
				return fmt.Errorf("--input and --output are required")
			}
			file, err := os.Open(input)
			if err != nil {
				return fmt.Errorf("open AMW input: %w", err)
			}
			defer file.Close()
			inputInfo, err := file.Stat()
			if err != nil {
				return fmt.Errorf("stat AMW input: %w", err)
			}
			if outputInfo, err := os.Stat(output); err == nil {
				if os.SameFile(inputInfo, outputInfo) {
					return fmt.Errorf("AMW output must not overwrite the input or an alias of it")
				}
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("stat AMW output: %w", err)
			}
			data, err := io.ReadAll(io.LimitReader(file, amwMaxInputBytes+1))
			if err != nil {
				return fmt.Errorf("read AMW input: %w", err)
			}
			if len(data) > amwMaxInputBytes {
				return fmt.Errorf("AMW input exceeds the 64 MiB limit")
			}
			var report amwReport
			// Unmarshal requires exactly one JSON value, rejecting trailing junk.
			if err := json.Unmarshal(data, &report); err != nil {
				return fmt.Errorf("decode AMW input: %w", err)
			}
			inputPath, err := filepath.Abs(input)
			if err != nil {
				return err
			}
			outputDir, err := filepath.Abs(filepath.Dir(output))
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(outputDir, inputPath)
			if err != nil {
				return fmt.Errorf("locate AMW evidence relative to report: %w", err)
			}
			// Encode filenames as URL paths (including spaces, # and ?), not HTML
			// or URL schemes. srcdoc inherits the containing report's base URL.
			evidenceURL := (&url.URL{Path: "./" + filepath.ToSlash(relative)}).String()
			html, err := renderAMWHTMLWithEvidence(report, evidenceURL)
			if err != nil {
				return err
			}
			// Render beside the destination so failed writes preserve any existing
			// output and the final rename is an atomic replacement.
			tmp, err := os.CreateTemp(filepath.Dir(output), filepath.Base(output)+".tmp-*")
			if err != nil {
				return fmt.Errorf("create AMW output temporary file: %w", err)
			}
			defer os.Remove(tmp.Name())
			if err := tmp.Close(); err != nil {
				return fmt.Errorf("close AMW output temporary file: %w", err)
			}
			if err := renderObservabilityPage(tmp.Name(), []observabilityTab{{Title: "AMW", HTML: string(html)}}); err != nil {
				return err
			}
			if err := os.Chmod(tmp.Name(), 0644); err != nil {
				return fmt.Errorf("chmod AMW output: %w", err)
			}
			if err := os.Rename(tmp.Name(), output); err != nil {
				return fmt.Errorf("replace AMW output: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "Saved AMW JSON report (at most 64 MiB).")
	cmd.Flags().StringVar(&output, "output", "", "Output HTML file containing a single AMW tab.")
	return cmd
}
