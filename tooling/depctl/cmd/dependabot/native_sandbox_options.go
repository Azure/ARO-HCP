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
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/dependabot"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge/ghapi"
)

type rawNativeSandboxOptions struct{}

type validatedNativeSandboxOptions struct{}

type nativeSandboxOptions struct {
	endpoint string
	pin      string
	read     dependabot.NativeReadFunc
	output   io.Writer
}

// Validate rejects visible GitHub credentials before any sandbox read.
func (o *rawNativeSandboxOptions) Validate() (*validatedNativeSandboxOptions, error) {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "CI_TOKEN", "COPILOT_GITHUB_TOKEN", "GITHUB_MCP_SERVER_TOKEN"} {
		if os.Getenv(name) != "" {
			return nil, cli.Invalid(errors.New("GitHub credentials must not be visible in the sandbox"))
		}
	}
	return &validatedNativeSandboxOptions{}, nil
}

// Complete binds the fixed release identity, isolated reader and command output.
func (o *validatedNativeSandboxOptions) Complete(output io.Writer) *nativeSandboxOptions {
	return &nativeSandboxOptions{
		endpoint: "repos/actions/checkout/commits/v7.0.1",
		pin:      "3d3c42e5aac5ba805825da76410c181273ba90b1",
		read:     ghapi.NativeCLIRead,
		output:   output,
	}
}

// Run verifies the official release identity before writing its JSON receipt.
func (o *nativeSandboxOptions) Run(ctx context.Context) error {
	payload, err := o.read(ctx, o.endpoint, false, false)
	if err != nil {
		return err
	}
	var release struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(payload, &release); err != nil {
		return err
	}
	if release.SHA != o.pin {
		return errors.New("sandbox cannot verify the official checkout release")
	}
	return json.NewEncoder(o.output).Encode(map[string]string{"status": "verified"})
}
