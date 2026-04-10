// Copyright 2025 Microsoft Corporation
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

package buildlog

import (
	"context"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/config"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/gcs"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/prow"
)

// Result holds the build log fetch result.
type Result struct {
	Step       string   `json:"step"`
	Container  string   `json:"container"`
	Lines      []string `json:"lines"`
	TotalLines int      `json:"total_lines"`
}

// Fetch retrieves and processes a build log from GCS.
func Fetch(ctx context.Context, client *gcs.Client, baseURL, env, step string, lines int) (*Result, error) {
	cfg, ok := config.Envs[env]
	if !ok {
		return nil, &config.UnknownEnvError{Env: env}
	}

	container := cfg.Container
	if step == "provision" {
		container = config.ProvisionContainer
	}

	text, err := client.FetchBuildLog(ctx, baseURL, cfg.Step, container)
	if err != nil {
		return nil, err
	}
	if text == "" {
		return nil, nil
	}

	text = prow.CleanBuildLog(text)
	allLines := strings.Split(text, "\n")
	total := len(allLines)

	tail := allLines
	if lines > 0 && lines < total {
		tail = allLines[total-lines:]
	}

	return &Result{
		Step:       cfg.Step,
		Container:  container,
		Lines:      tail,
		TotalLines: total,
	}, nil
}
