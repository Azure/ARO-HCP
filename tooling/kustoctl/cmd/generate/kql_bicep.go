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

package generate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"text/template"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/kustoctl/pkg/kqlfunc"
)

// templateData is the data passed to the main.bicep template.
type templateData struct {
	// ServiceLogsFunctions is the relative loadTextContent() path for every
	// ServiceLogs function, in a valid dependency load order.
	ServiceLogsFunctions []string
	// HostedControlPlaneLogsFunctions is the same, for HostedControlPlaneLogs.
	HostedControlPlaneLogsFunctions []string
}

func newKQLBicepCommand() *cobra.Command {
	var functionsDir, templatePath, outputPath string

	cmd := &cobra.Command{
		Use:   "kql-bicep",
		Short: "Render main.bicep from main.bicep.tmpl and the current .kql function files",
		Long: `kql-bicep walks a directory of Kusto function ".kql" files, computes a
valid dependency load order per database via kqlfunc (the same mechanism used
by "kustoctl validate kql"), and renders a bicep template with the resulting
loadTextContent() lists. The rendered file must be committed; run this after
adding, removing, or re-dependencying a function file, and commit the result.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKQLBicep(functionsDir, templatePath, outputPath)
		},
	}

	cmd.Flags().StringVar(&functionsDir, "functions-dir", "", "Directory containing \"{Database}_{FunctionName}.kql\" function files (required)")
	cmd.Flags().StringVar(&templatePath, "template", "", "Path to the bicep template file (required)")
	cmd.Flags().StringVar(&outputPath, "output", "", "Path to write the rendered bicep file (required)")
	for _, name := range []string{"functions-dir", "template", "output"} {
		_ = cmd.MarkFlagRequired(name)
	}

	return cmd
}

func runKQLBicep(functionsDir, templatePath, outputPath string) error {
	paths, err := kqlfunc.FindFiles(functionsDir)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("no .kql files found under %q", functionsDir)
	}

	files, err := kqlfunc.ParseFiles(paths)
	if err != nil {
		return fmt.Errorf("parsing function files: %w", err)
	}
	grouped, err := kqlfunc.SortByDependency(files)
	if err != nil {
		return fmt.Errorf("ordering function files: %w", err)
	}

	outputDir := filepath.Dir(outputPath)
	relativePaths := func(database string) ([]string, error) {
		rels := make([]string, 0, len(grouped[database]))
		for _, f := range grouped[database] {
			rel, err := filepath.Rel(outputDir, f.Path)
			if err != nil {
				return nil, fmt.Errorf("computing path for %q relative to %q: %w", f.Path, outputDir, err)
			}
			rels = append(rels, filepath.ToSlash(rel))
		}
		return rels, nil
	}

	serviceLogs, err := relativePaths("ServiceLogs")
	if err != nil {
		return err
	}
	hostedControlPlaneLogs, err := relativePaths("HostedControlPlaneLogs")
	if err != nil {
		return err
	}

	knownDatabases := map[string]bool{"ServiceLogs": true, "HostedControlPlaneLogs": true}
	var unknown []string
	for database := range grouped {
		if !knownDatabases[database] {
			unknown = append(unknown, database)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("found function file(s) for database(s) %v under %q, which main.bicep.tmpl does not render a loadTextContent() list for; add one", unknown, functionsDir)
	}

	tmplContent, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("reading template %q: %w", templatePath, err)
	}
	tmpl, err := template.New(filepath.Base(templatePath)).Parse(string(tmplContent))
	if err != nil {
		return fmt.Errorf("parsing template %q: %w", templatePath, err)
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("creating %q: %w", outputPath, err)
	}
	defer out.Close()

	if err := tmpl.Execute(out, templateData{
		ServiceLogsFunctions:            serviceLogs,
		HostedControlPlaneLogsFunctions: hostedControlPlaneLogs,
	}); err != nil {
		return fmt.Errorf("rendering template: %w", err)
	}

	return nil
}
