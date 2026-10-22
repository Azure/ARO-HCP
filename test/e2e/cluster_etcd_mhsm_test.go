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

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMHSMCLIUsesCustomerSubscription(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	cliName := "az"
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"$MHSM_TEST_ARGS\"\n" +
		"if [ \"$2\" = show ]; then printf 'Active\\n'; fi\n" +
		"if [ \"$4\" = list ]; then printf '[]\\n'; fi\n"
	if runtime.GOOS == "windows" {
		cliName = "az.cmd"
		script = "@echo off\r\n" +
			"echo %*>>\"%MHSM_TEST_ARGS%\"\r\n" +
			"if \"%~2\"==\"show\" echo Active\r\n" +
			"if \"%~4\"==\"list\" echo []\r\n" +
			"exit /b 0\r\n"
	}
	if err := os.WriteFile(filepath.Join(dir, cliName), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MHSM_TEST_ARGS", argsFile)

	ctx := context.Background()
	const subscriptionID = "customer-subscription"
	status, err := mhsmActivationStatus(ctx, subscriptionID, "customer-rg", "test-hsm")
	if err != nil {
		t.Fatal(err)
	}
	if status != mhsmActivationStatusActive {
		t.Fatalf("activation status = %q, want %q", status, mhsmActivationStatusActive)
	}
	if err := downloadMHSMSecurityDomain(ctx, subscriptionID, "test-hsm", filepath.Join(dir, "security-domain.json"),
		[]string{"cert-0.cer", "cert-1.cer", "cert-2.cer"}, 2); err != nil {
		t.Fatal(err)
	}
	if err := ensureMHSMCryptoUser(ctx, subscriptionID, "test-hsm", "principal-id", "ServicePrincipal", "/keys"); err != nil {
		t.Fatal(err)
	}

	recorded, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	commands := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(recorded), "\r\n", "\n")), "\n")
	wantPrefixes := []string{
		"keyvault show ",
		"keyvault security-domain download ",
		"keyvault role assignment list ",
		"keyvault role assignment create ",
	}
	if len(commands) != len(wantPrefixes) {
		t.Fatalf("got %d commands, want %d: %s", len(commands), len(wantPrefixes), recorded)
	}
	for i, command := range commands {
		if !strings.HasPrefix(command, wantPrefixes[i]) {
			t.Errorf("command %d = %q, want prefix %q", i, command, wantPrefixes[i])
		}
		if !strings.Contains(command, "--subscription "+subscriptionID+" ") {
			t.Errorf("command %d does not select the customer subscription: %s", i, command)
		}
		if !strings.Contains(command, "--hsm-name test-hsm ") {
			t.Errorf("command %d does not select the HSM: %s", i, command)
		}
	}
}
