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

package ghapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"unicode/utf8"
)

// NativeCLIRead uses the supported gh proxy for read-only sandbox API evidence.
// It does not require or create a credential in the sandbox process.
func NativeCLIRead(ctx context.Context, endpoint string, paginated, ci bool) (json.RawMessage, error) {
	location, err := url.Parse(endpoint)
	if err != nil || !strings.HasPrefix(endpoint, "repos/") || location.IsAbs() || location.Host != "" || location.Fragment != "" {
		return nil, errors.New("native evidence requires a relative repository API endpoint")
	}
	arguments := []string{"api", "--method", "GET"}
	if paginated {
		arguments = append(arguments, "--paginate", "--slurp")
	}
	command := exec.CommandContext(ctx, "gh", append(arguments, endpoint)...)
	if token := os.Getenv("CI_TOKEN"); ci && token != "" {
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "GH_TOKEN=") {
				command.Env = append(command.Env, entry)
			}
		}
		command.Env = append(command.Env, "GH_TOKEN="+token)
	}
	payload, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("native GitHub GET failed: %w", err)
	}
	if !utf8.Valid(payload) || !json.Valid(payload) {
		return nil, errors.New("native evidence response is not complete UTF-8 JSON")
	}
	return payload, nil
}
