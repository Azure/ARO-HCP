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
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	identitypool "github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/identity-pool"
)

type recordingPoolHandler struct {
	*identitypool.Handler
	requests []assets.PoolRequest
}

func (h *recordingPoolHandler) ApplyPools(_ context.Context, request assets.PoolRequest) error {
	h.requests = append(h.requests, request)
	return nil
}

func (h *recordingPoolHandler) ValidatePools(_ context.Context, request assets.PoolRequest) error {
	h.requests = append(h.requests, request)
	return nil
}

func TestAssetCommandsPreserveUnmanagedSelection(t *testing.T) {
	t.Parallel()
	catalogPath := filepath.Join(t.TempDir(), "catalog.yaml")
	const catalog = `version: 2
environments:
  dev:
    deployment_environment: {name: ci01, infrastructure_subscription: infra}
    pools:
    - name: unmanaged
      subscriptions: {e2e: customer}
      region: westus3
      slot_count: 1
      slot_assets:
        e2e_identities:
          allocation: dedicated
          provisioning: unmanaged
          resource_group_prefix: identities
          resource_group_count: 1
`
	if err := os.WriteFile(catalogPath, []byte(catalog), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, validate := range []bool{false, true} {
		for _, selector := range []string{"default", "subscription", "pool", "legacy-subscription", "missing-pool", "noncanonical-asset"} {
			t.Run(map[bool]string{false: "apply", true: "validate"}[validate]+"/"+selector, func(t *testing.T) {
				handler := &recordingPoolHandler{Handler: identitypool.NewHandler()}
				registry, err := assets.NewRegistry(handler)
				if err != nil {
					t.Fatal(err)
				}
				constructor := newApplyPoolAssetsCommand
				if validate {
					constructor = newValidatePoolAssetsCommand
				}
				command, err := constructor(registry)
				if err != nil {
					t.Fatal(err)
				}
				args := []string{"--environment", "dev", "--slot-catalog", catalogPath, "--asset", "e2e_identities"}
				switch selector {
				case "subscription":
					args = append(args, "--subscription", "customer")
				case "pool":
					args = append(args, "--pool", "unmanaged")
				case "legacy-subscription":
					command, err = newIdentityPoolCompatibilityCommand(registry, validate)
					if err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(command.Deprecated, "--asset e2e_identities") {
						t.Fatalf("deprecation must recommend canonical selector: %q", command.Deprecated)
					}
					args = []string{"--environment", "dev", "--slot-catalog", catalogPath, "--subscription", "customer"}
				case "missing-pool":
					args = append(args, "--pool", "missing")
				case "noncanonical-asset":
					args[len(args)-1] = "e2e-identities"
				}
				command.SetOut(io.Discard)
				command.SetErr(io.Discard)
				command.SetArgs(args)
				err = command.Execute()
				wantError := ""
				switch selector {
				case "missing-pool":
					wantError = "no pools matched"
				case "noncanonical-asset":
					wantError = `unknown asset kind "e2e-identities"`
				}
				if wantError != "" {
					if err == nil || !strings.Contains(err.Error(), wantError) || len(handler.requests) != 0 {
						t.Fatalf("expected %q before handler, got error=%v requests=%v", wantError, err, handler.requests)
					}
					return
				}
				if err != nil {
					t.Fatalf("executing command: %v", err)
				}
				if len(handler.requests) != 1 {
					t.Fatalf("expected one pool operation, got %d", len(handler.requests))
				}
				request := handler.requests[0]
				if request.IncludeUnmanaged != (selector != "default") || len(request.Pools) != 1 || !request.Pools[0].IsUnmanaged() {
					t.Fatalf("incorrect selection policy passed to handler: %+v", request)
				}
			})
		}
	}
}
