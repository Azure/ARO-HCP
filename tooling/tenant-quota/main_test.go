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

package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/cijoboutcomes"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
	prowmetrics "github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/prow"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/prowjobs"
)

type prowClientFunc func(context.Context) ([]prowjobs.Job, error)

func (f prowClientFunc) ListJobs(ctx context.Context) ([]prowjobs.Job, error) {
	return f(ctx)
}

func TestCollectorRestartsAfterRecoveredPanic(t *testing.T) {
	originalCrash := utilruntime.ReallyCrash
	utilruntime.ReallyCrash = false
	defer func() { utilruntime.ReallyCrash = originalCrash }()
	cfg := &config.Config{
		Tenants: []config.TenantConfig{{TenantID: "tenant", ClientIDKeyVaultSecretName: "client", ClientSecretKeyVaultSecretName: "secret"}},
		Prow: config.ProwConfig{
			Enabled: true, Interval: "1h", BaseURL: "https://prow.example.com",
			Repository: config.ProwRepositoryConfig{Org: "Azure", Name: "ARO-HCP"},
		},
	}
	require.NoError(t, cfg.Validate())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var calls int
	var firstAttempt, secondAttempt time.Time
	collector := prowmetrics.NewCollector(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), prowClientFunc(func(context.Context) ([]prowjobs.Job, error) {
		calls++
		if calls == 1 {
			firstAttempt = time.Now()
			panic("collection failed")
		}
		secondAttempt = time.Now()
		cancel()
		return nil, nil
	}))
	runCollector(ctx, collector.Start)
	require.Equal(t, 2, calls, "a recovered panic must not permanently stop collection")
	require.GreaterOrEqual(t, secondAttempt.Sub(firstAttempt), time.Second, "restart must back off rather than spin")
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)
	metrics, err := registry.Gather()
	require.NoError(t, err)
	for _, metric := range metrics {
		if metric.GetName() == "prow_ci_collection_success" {
			require.Equal(t, float64(1), metric.Metric[0].GetGauge().GetValue(), "restarted collector must publish fresh metrics")
			return
		}
	}
	t.Fatal("collection success metric missing")
}

func TestCollectorCancellationDuringRestartDelay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan struct{})
	finished := make(chan struct{})
	var calls int
	go func() {
		defer close(finished)
		runCollector(ctx, func(context.Context) {
			calls++
			if calls == 1 {
				close(returned)
			}
		})
	}()
	<-returned
	cancel()
	select {
	case <-finished:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("cancellation did not interrupt collector restart delay")
	}
	require.Equal(t, 1, calls, "cancelled collectors must not restart")
	runCollector(ctx, func(context.Context) { t.Fatal("already cancelled collector started") })
}

func TestPanicPolicy(t *testing.T) {
	originalCrash, originalHandlers := utilruntime.ReallyCrash, utilruntime.PanicHandlers
	t.Cleanup(func() {
		utilruntime.ReallyCrash, utilruntime.PanicHandlers = originalCrash, originalHandlers
		panicMetricsOnce = sync.Once{}
	})
	for _, tc := range []struct {
		name, yaml string
		fatal      bool
	}{
		{name: "default"},
		{name: "explicit false", yaml: "exitOnPanic: false"},
		{name: "explicit true", yaml: "exitOnPanic: true", fatal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg config.Config
			require.NoError(t, yaml.Unmarshal([]byte(tc.yaml), &cfg))
			configurePanicHandling(cfg.ExitOnPanic)
			configurePanicHandling(cfg.ExitOnPanic)
			require.Equal(t, tc.fatal, utilruntime.ReallyCrash)
			require.Len(t, utilruntime.PanicHandlers, len(originalHandlers)+1)
			for i, handler := range originalHandlers {
				require.Equal(t, reflect.ValueOf(handler).Pointer(), reflect.ValueOf(utilruntime.PanicHandlers[i]).Pointer(), "existing panic logging must remain installed")
			}

			var logs bytes.Buffer
			ctx := utils.ContextWithLogger(context.Background(), logr.FromSlogHandler(slog.NewJSONHandler(&logs, nil)))
			ctx = utils.ContextWithControllerName(ctx, t.Name())
			counter := utils.PanicTotal.WithLabelValues(t.Name())
			before := testutil.ToFloat64(counter)
			panicOnce := func() {
				defer utilruntime.HandleCrashWithContext(ctx)
				panic("test panic policy")
			}
			if tc.fatal {
				require.PanicsWithValue(t, "test panic policy", panicOnce)
			} else {
				require.NotPanics(t, panicOnce)
			}
			require.Equal(t, before+1, testutil.ToFloat64(counter), "each panic must be counted exactly once")
			require.Contains(t, logs.String(), "Observed a panic")
		})
	}
}

func TestRunImageIdentityMapping(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		info     string
		uid      string
		identity []string
	}{
		{name: "rootless podman", info: `{"host":{"security":{"rootless":true}}}`, identity: []string{"--userns=keep-id:uid=65532,gid=65532", "--user", "65532:65532"}},
		{name: "docker compatibility wrapper", info: `{"host":{"security":{"rootless":true}}}`, identity: []string{"--userns=keep-id:uid=65532,gid=65532", "--user", "65532:65532"}},
		{name: "rootful docker", info: `{"SecurityOptions":["name=seccomp,profile=builtin"]}`, identity: []string{"--user", "10001:10002"}},
		{name: "rootless docker", info: `{"SecurityOptions":["name=rootless"]}`},
		{name: "remapped docker", info: `{"SecurityOptions":["name=userns"]}`},
		{name: "rootful podman", info: `{"host":{"security":{"rootless":false}}}`},
		{name: "unknown engine", info: `{}`},
		{name: "invalid info", info: `not-json`},
		{name: "malformed docker options", info: `{"SecurityOptions":[null]}`},
		{name: "root caller", info: `{"SecurityOptions":[]}`, uid: "0"},
		{name: "unavailable engine", info: "failure"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			directory := t.TempDir()
			engine := filepath.Join(directory, "container engine")
			if scenario.name == "docker compatibility wrapper" {
				engine = filepath.Join(directory, "docker")
			}
			require.NoError(t, os.WriteFile(engine, []byte(`#!/bin/bash
set -euo pipefail
if [[ "$1" == info ]]; then
    [[ "$ENGINE_INFO" != failure ]] || exit 1
    printf '%s' "$ENGINE_INFO"
elif [[ "$1" == run ]]; then
    printf '%s\0' "$@" > "$ARGS_FILE"
else
    exit 99
fi
`), 0755))
			require.NoError(t, os.WriteFile(filepath.Join(directory, "id"), []byte("#!/bin/bash\nif [[ $1 == -u ]]; then echo \"$TEST_UID\"; else echo 10002; fi\n"), 0755))
			uid := scenario.uid
			if uid == "" {
				uid = "10001"
			}
			argumentsFile := filepath.Join(directory, "args")
			command := exec.Command("make", "--no-print-directory", "-s", "-o", "image", "-o", "_output/config.yaml", "-o", "fetch-secrets", "run-image", "CONTAINER_ENGINE="+engine, "TENANT_QUOTA_TAGGED_IMAGE=synthetic-image")
			command.Env = append(os.Environ(), "PATH="+directory+":"+os.Getenv("PATH"), "ENGINE_INFO="+scenario.info, "TEST_UID="+uid, "ARGS_FILE="+argumentsFile)
			output, err := command.CombinedOutput()
			if scenario.identity == nil {
				require.Error(t, err, "%s", output)
				_, statErr := os.Stat(argumentsFile)
				require.True(t, os.IsNotExist(statErr), "unsupported mode must not launch a container")
				return
			}
			require.NoError(t, err, "%s", output)
			arguments, err := os.ReadFile(argumentsFile)
			require.NoError(t, err)
			configPath, err := filepath.Abs("_output/config.yaml")
			require.NoError(t, err)
			secretsPath, err := filepath.Abs("_output/secrets")
			require.NoError(t, err)
			expected := append([]string{"run", "--rm"}, scenario.identity...)
			expected = append(expected, "-p", "8080:8080", "-e", "CONFIG_PATH=/config.yaml", "-e", "SECRETS_STORE_PATH=/secrets", "-v", configPath+":/config.yaml:ro", "-v", secretsPath+":/secrets:ro", "synthetic-image")
			require.Equal(t, expected, strings.Split(strings.TrimSuffix(string(arguments), "\x00"), "\x00"))
		})
	}
}

func TestCredentialPairPublicationScripts(t *testing.T) {
	for _, scenario := range []string{"new", "encoded-password", "mismatch", "verify-failure", "malformed-token", "partial-retry", "readback-failure", "handoff", "wrong-target", "lookup-failure", "handoff-permissions", "handoff-symlink", "handoff-type", "handoff-malformed", "handoff-multiple", "legacy-mismatch"} {
		t.Run(scenario, func(t *testing.T) {
			command := exec.Command("bash", "-euo", "pipefail", "-c", `
source scripts/credential-pair.sh
KEYVAULT_NAME=test-vault
PAIR_TENANT_ID=22222222-2222-2222-2222-222222222222
PAIR_CLIENT_ID=11111111-1111-1111-1111-111111111111
PAIR_SECRET_NAME=test-client-secret
PAIR_ID_NAME=test-client-id
PAIR_PASSWORD=test-password
if [[ "$SCENARIO" == encoded-password ]]; then PAIR_PASSWORD='test +&?%'; fi
curl() {
    if [[ "$SCENARIO" == verify-failure ]]; then echo sensitive-error >&2; return 1; fi
    if [[ "$SCENARIO" == malformed-token ]]; then echo '{"access_token":sensitive-error}'; return 0; fi
    local form="" encoded_password=test-password
    while [[ $# -gt 0 ]]; do
        if [[ "$1" == --data-binary ]]; then form="${2#@}"; break; fi
        shift
    done
    if [[ "$SCENARIO" == encoded-password ]]; then encoded_password='test%20%2B%26%3F%25'; fi
    printf '%s' "grant_type=client_credentials&scope=https%3A%2F%2Fmanagement.azure.com%2F.default&client_id=$PAIR_CLIENT_ID&client_secret=$encoded_password" | cmp -s - "$form" || return 1
    echo '{"access_token":"test-token"}'
}
az() {
    local operation="$3" name="" file=""
    shift 3
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --name) name="$2"; shift 2 ;;
            --file) file="$2"; shift 2 ;;
            *) shift ;;
        esac
    done
    if [[ "$operation" == show ]]; then
        if [[ "$SCENARIO" == lookup-failure ]]; then echo Forbidden >&2; return 1; fi
        if [[ "$SCENARIO" == readback-failure && -f "$TEST_DIR/$PAIR_ID_NAME" ]]; then echo Forbidden >&2; return 1; fi
        if [[ -f "$TEST_DIR/$name" ]]; then cat "$TEST_DIR/$name"; else echo SecretNotFound >&2; return 1; fi
    else
        if [[ "$SCENARIO" == partial-retry && "$name" == "$PAIR_ID_NAME" ]]; then return 1; fi
        cp "$file" "$TEST_DIR/$name"
    fi
}
case "$SCENARIO" in
    mismatch)
        echo old-id > "$TEST_DIR/$PAIR_ID_NAME"
        if publish_pair; then exit 1; fi
        [[ ! -f "$TEST_DIR/$PAIR_SECRET_NAME" ]]
        ;;
    verify-failure|lookup-failure|malformed-token)
        if publish_pair; then exit 1; fi
        [[ ! -f "$TEST_DIR/$PAIR_SECRET_NAME" ]]
        ;;
    legacy-mismatch)
        printf '%s' old-password > "$TEST_DIR/$PAIR_SECRET_NAME"
        if publish_pair; then exit 1; fi
        [[ "$(cat "$TEST_DIR/$PAIR_SECRET_NAME")" == old-password && ! -f "$TEST_DIR/$PAIR_ID_NAME" ]]
        ;;
    readback-failure)
        if publish_pair; then exit 1; fi
        SCENARIO=new
        publish_pair
        ;;
    partial-retry)
        if publish_pair; then exit 1; fi
        [[ -f "$TEST_DIR/$PAIR_SECRET_NAME" && ! -f "$TEST_DIR/$PAIR_ID_NAME" ]]
        SCENARIO=new
        publish_pair
        ;;
    handoff|wrong-target|handoff-permissions|handoff-symlink|handoff-type|handoff-malformed|handoff-multiple)
        save_pair "$TEST_DIR/pair.json"
        [[ "$(stat -c %a "$TEST_DIR/pair.json")" == 600 ]]
        if save_pair "$TEST_DIR/pair.json"; then exit 1; fi
        case "$SCENARIO" in
            handoff-permissions) chmod 644 "$TEST_DIR/pair.json" ;;
            handoff-symlink) mv "$TEST_DIR/pair.json" "$TEST_DIR/target.json"; ln -s "$TEST_DIR/target.json" "$TEST_DIR/pair.json" ;;
            handoff-type) jq '.clientSecret = 123' "$TEST_DIR/pair.json" > "$TEST_DIR/modified.json"; mv "$TEST_DIR/modified.json" "$TEST_DIR/pair.json" ;;
            handoff-malformed) printf '%s' '{"clientSecret":sensitive-error}' > "$TEST_DIR/pair.json" ;;
            handoff-multiple) cp "$TEST_DIR/pair.json" "$TEST_DIR/copy.json"; cat "$TEST_DIR/copy.json" >> "$TEST_DIR/pair.json" ;;
        esac
        if [[ "$SCENARIO" == handoff-* ]]; then
            if upload_pair "$TEST_DIR/pair.json" "$PAIR_SECRET_NAME"; then exit 1; fi
            [[ ! -f "$TEST_DIR/$PAIR_SECRET_NAME" ]]
            exit 0
        fi
        if [[ "$SCENARIO" == wrong-target ]]; then
            if upload_pair "$TEST_DIR/pair.json" wrong-client-secret; then exit 1; fi
            [[ ! -f "$TEST_DIR/$PAIR_SECRET_NAME" ]]
        else
            upload_pair "$TEST_DIR/pair.json" "$PAIR_SECRET_NAME"
        fi
        ;;
    *) publish_pair ;;
esac
`)
			command.Env = append(os.Environ(), "TEST_DIR="+t.TempDir(), "SCENARIO="+scenario)
			output, err := command.CombinedOutput()
			require.NoError(t, err, "%s", output)
			require.NotContains(t, string(output), "test-password")
			require.NotContains(t, string(output), "test-token")
			require.NotContains(t, string(output), "sensitive-error")
		})
	}
}

func TestCredentialPairStagingFailures(t *testing.T) {
	for _, scenario := range []string{"success", "value-temp", "value-copy", "value-partial", "form-temp", "form-partial"} {
		t.Run(scenario, func(t *testing.T) {
			command := exec.Command("bash", "-euo", "pipefail", "-c", `
source scripts/credential-pair.sh
KEYVAULT_NAME=test-vault
PAIR_TENANT_ID=22222222-2222-2222-2222-222222222222
PAIR_CLIENT_ID=11111111-1111-1111-1111-111111111111
PAIR_SECRET_NAME=test-client-secret
PAIR_ID_NAME=test-client-id
PAIR_PASSWORD=synthetic-new-password
mkdir "$TEST_DIR/vault"
printf '%s' "$PAIR_CLIENT_ID" > "$TEST_DIR/vault/$PAIR_ID_NAME"
printf '%s' synthetic-old-password > "$TEST_DIR/vault/$PAIR_SECRET_NAME"
save_pair "$TEST_DIR/pair.json"
cp "$TEST_DIR/pair.json" "$TEST_DIR/pair.original"
mktemp() {
    case "$SCENARIO:$1" in
        value-temp:*/value.*|form-temp:*/form.*) return 1 ;;
    esac
    command mktemp "$@"
}
cat() {
    if [[ $# == 0 && "$SCENARIO" == value-* ]]; then
        command cat >/dev/null
        if [[ "$SCENARIO" == value-partial ]]; then printf '%s' synthetic-partial; fi
        return 1
    fi
    command cat "$@"
}
jq() {
    if [[ "$SCENARIO" == form-partial && "$*" == '-Rs --arg client '* ]]; then
        command cat >/dev/null
        printf '%s' synthetic-partial
        return 1
    fi
    command jq "$@"
}
curl() {
    touch "$TEST_DIR/token-called"
    printf '%s' '{"access_token":"synthetic-token"}'
}
az() {
    local operation="$3" name="" file=""
    shift 3
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --name) name="$2"; shift 2 ;;
            --file) file="$2"; shift 2 ;;
            *) shift ;;
        esac
    done
    if [[ "$operation" == show ]]; then
        command cat "$TEST_DIR/vault/$name"
    else
        touch "$TEST_DIR/set-called"
        [[ "$(stat -c %a "$file")" == 600 ]] || return 1
        cp "$file" "$TEST_DIR/vault/$name"
    fi
}
if upload_pair "$TEST_DIR/pair.json" "$PAIR_SECRET_NAME"; then
    [[ "$SCENARIO" == success ]] || { echo 'Unexpected publication success'; exit 1; }
    [[ -f "$TEST_DIR/set-called" && -f "$TEST_DIR/token-called" ]]
    [[ "$(command cat "$TEST_DIR/vault/$PAIR_SECRET_NAME")" == "$PAIR_PASSWORD" ]]
else
    [[ "$SCENARIO" != success ]] || exit 1
    [[ ! -f "$TEST_DIR/set-called" ]] || { echo 'Unexpected vault set'; exit 1; }
    [[ "$(command cat "$TEST_DIR/vault/$PAIR_SECRET_NAME")" == synthetic-old-password ]]
    if [[ "$SCENARIO" == form-* ]]; then
        [[ ! -f "$TEST_DIR/token-called" ]] || { echo 'Unexpected token request'; exit 1; }
    fi
fi
[[ "$(command cat "$TEST_DIR/vault/$PAIR_ID_NAME")" == "$PAIR_CLIENT_ID" ]]
cmp -s "$TEST_DIR/pair.json" "$TEST_DIR/pair.original"
[[ "$(stat -c %a "$TEST_DIR/pair.json")" == 600 ]]
[[ "$(stat -c %a "$PAIR_TEMP_DIR")" == 700 ]]
[[ -z "$(find "$PAIR_TEMP_DIR" -mindepth 1 -print -quit)" ]] || { echo 'Staging file not cleaned up'; exit 1; }
`)
			command.Env = append(os.Environ(), "TEST_DIR="+t.TempDir(), "SCENARIO="+scenario)
			output, err := command.CombinedOutput()
			require.NotContains(t, string(output), "synthetic-new-password")
			require.NotContains(t, string(output), "synthetic-old-password")
			require.NotContains(t, string(output), "synthetic-partial")
			require.NotContains(t, string(output), "synthetic-token")
			require.NoError(t, err, "%s", output)
		})
	}
}

func TestStartupRejectsMissingCredentialPair(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("tenants:\n- tenantId: test-tenant\n  clientIdKeyVaultSecretName: missing-client-id\n  clientSecretKeyVaultSecretName: missing-client-secret\n"), 0600))
	t.Setenv("SECRETS_STORE_PATH", directory)
	t.Setenv("CONFIG_PATH", configPath)
	err := run(slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.ErrorContains(t, err, "credential validation failed")
}

func TestRenewalScriptRecovery(t *testing.T) {
	for _, scenario := range []string{"success", "deferred", "existing-handoff", "expired-current", "reset-failure", "new-verification-failure", "write-failure", "readback-failure"} {
		t.Run(scenario, func(t *testing.T) {
			command := exec.Command("bash", "-euo", "pipefail", "-c", `
export TMPDIR="$TEST_DIR"
mkdir "$TEST_DIR/vault"
printf '%s' 11111111-1111-1111-1111-111111111111 > "$TEST_DIR/vault/test-client-id"
printf '%s' old-password > "$TEST_DIR/vault/test-client-secret"
printf '%s' '{"tenants":[{"tenantName":"test","tenantId":"22222222-2222-2222-2222-222222222222","clientIdKeyVaultSecretName":"test-client-id","clientSecretKeyVaultSecretName":"test-client-secret"}]}' > "$TEST_DIR/config.json"
touch "$TEST_DIR/operations"
yq() {
    [[ "$1" == -o=json && "$2" == .tenants ]] || return 99
    jq '.tenants' "$3"
}
az() {
    case "$1 $2" in
        'account show') echo 22222222-2222-2222-2222-222222222222; return ;;
        'ad app')
            [[ "$3 $4" == 'credential reset' && " $* " == *' --append '* && " $* " == *' --display-name tenant-quota-'* ]] || return 99
            if [[ "$SCENARIO" == reset-failure ]]; then echo sensitive-error >&2; return 1; fi
            echo mint >> "$TEST_DIR/operations"
            echo renewed-password
            return
            ;;
        'keyvault secret') ;;
        *) return 99 ;;
    esac
    local operation="$3" name="" file=""
    shift 3
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --name) name="$2"; shift 2 ;;
            --file) file="$2"; shift 2 ;;
            *) shift ;;
        esac
    done
    if [[ "$operation" == show ]]; then
        if [[ "$SCENARIO" == readback-failure && -f "$TEST_DIR/published" ]]; then echo sensitive-error >&2; return 1; fi
        cat "$TEST_DIR/vault/$name"
    elif [[ "$operation" == set ]]; then
        if [[ "$SCENARIO" == write-failure ]]; then echo sensitive-error >&2; return 1; fi
        cp "$file" "$TEST_DIR/vault/$name"
        touch "$TEST_DIR/published"
        echo publish >> "$TEST_DIR/operations"
    else
        return 99
    fi
}
curl() {
    local form=""
    while [[ $# -gt 0 ]]; do
        if [[ "$1" == --data-binary ]]; then form="${2#@}"; break; fi
        shift
    done
    [[ -n "$form" ]] || return 99
    if [[ "$SCENARIO" == expired-current ]]; then return 1; fi
    if [[ "$SCENARIO" == new-verification-failure ]] && grep -q 'client_secret=renewed-password' "$form"; then return 1; fi
    echo '{"access_token":"test-token"}'
}
kubectl() {
    [[ -f "$TEST_DIR/published" ]] || return 99
    echo "$1 $2" >> "$TEST_DIR/operations"
}
export -f az curl yq kubectl
arguments=(--tenant test --config "$TEST_DIR/config.json" --keyvault test-vault --yes)
case "$SCENARIO" in
    deferred|existing-handoff)
        arguments+=(--defer-secret-out "$TEST_DIR/handoff.json")
        if [[ "$SCENARIO" == existing-handoff ]]; then echo sentinel > "$TEST_DIR/handoff.json"; fi
        ;;
    *) arguments+=(--restart) ;;
esac
if bash scripts/renew-sp-secret.sh "${arguments[@]}"; then
    [[ "$SCENARIO" == success || "$SCENARIO" == deferred ]]
else
    [[ "$SCENARIO" != success && "$SCENARIO" != deferred ]]
fi
case "$SCENARIO" in
    existing-handoff|expired-current|reset-failure)
        [[ ! -s "$TEST_DIR/operations" && ! -f "$TEST_DIR/published" ]]
        if [[ "$SCENARIO" == existing-handoff ]]; then [[ "$(cat "$TEST_DIR/handoff.json")" == sentinel ]]; fi
        ;;
    success)
        [[ "$(cat "$TEST_DIR/operations")" == $'mint\npublish\nrollout restart\nrollout status' ]]
        [[ -z "$(find "$TEST_DIR" -name pair.json)" ]]
        ;;
    *)
        [[ "$(grep -c '^mint$' "$TEST_DIR/operations")" == 1 ]]
        if grep -q rollout "$TEST_DIR/operations"; then exit 1; fi
        handoff="$TEST_DIR/handoff.json"
        if [[ "$SCENARIO" != deferred ]]; then handoff=$(find "$TEST_DIR" -name pair.json); fi
        [[ -f "$handoff" && "$(stat -c %a "$handoff")" == 600 ]]
        SCENARIO=success bash scripts/manage-service-principals.sh --keyvault-only --keyvault test-vault --secret-name test-client-secret --secret-file "$handoff"
        [[ "$(grep -c '^mint$' "$TEST_DIR/operations")" == 1 ]]
        [[ "$(cat "$TEST_DIR/vault/test-client-secret")" == renewed-password ]]
        ;;
esac
`)
			command.Env = append(os.Environ(), "TEST_DIR="+t.TempDir(), "SCENARIO="+scenario)
			output, err := command.CombinedOutput()
			require.NoError(t, err, "%s", output)
			for _, sensitive := range []string{"old-password", "renewed-password", "test-token", "sensitive-error"} {
				require.NotContains(t, string(output), sensitive)
			}
		})
	}
}

func TestBootstrapScriptRecovery(t *testing.T) {
	for _, scenario := range []string{"success", "new-sp", "grant-failure", "ambiguous-subscription", "wrong-tenant", "existing-handoff", "deferred", "existing-pair", "staged", "direct", "token-failure", "write-failure", "readback-failure", "lookup-failure", "mismatched-binding"} {
		t.Run(scenario, func(t *testing.T) {
			command := exec.Command("bash", "-euo", "pipefail", "-c", `
export TMPDIR="$TEST_DIR"
mkdir "$TEST_DIR/vault"
printf '%s' '{"tenants":[{"tenantName":"TestTestARO","tenantId":"22222222-2222-2222-2222-222222222222","clientIdKeyVaultSecretName":"test-client-id","clientSecretKeyVaultSecretName":"test-client-secret"}]}' > "$TEST_DIR/config.json"
touch "$TEST_DIR/operations"
case "$SCENARIO" in
    existing-pair|staged)
        printf '%s' 11111111-1111-1111-1111-111111111111 > "$TEST_DIR/vault/test-client-id"
        printf '%s' old-password > "$TEST_DIR/vault/test-client-secret"
        ;;
    mismatched-binding) echo 33333333-3333-3333-3333-333333333333 > "$TEST_DIR/vault/test-client-id" ;;
esac
yq() { [[ "$1" == -o=json && "$2" == .tenants ]] || return 99; jq '.tenants' "$3"; }
az() {
    case "$1 $2 $3" in
        'account show --query')
            [[ "$SCENARIO" != upload ]] || return 99
            if [[ "$SCENARIO" == wrong-tenant ]]; then echo 33333333-3333-3333-3333-333333333333; else echo 22222222-2222-2222-2222-222222222222; fi
            return ;;
        'account list --query')
            [[ "$4" == *"tenantId=='22222222-2222-2222-2222-222222222222'"* ]] || return 99
            if [[ "$SCENARIO" == ambiguous-subscription ]]; then echo 55555555-5555-5555-5555-555555555555; fi
            echo 44444444-4444-4444-4444-444444444444; return ;;
        'ad app list')
            if [[ "$SCENARIO" != new-sp && "$SCENARIO" != grant-failure ]]; then echo 11111111-1111-1111-1111-111111111111; fi
            return ;;
        'ad sp create-for-rbac')
            [[ " $* " == *' --create-password false '* ]] || return 99
            echo create >> "$TEST_DIR/operations"
            echo '{"appId":"11111111-1111-1111-1111-111111111111"}'
            return ;;
        'role assignment create')
            [[ "$SCENARIO" != grant-failure ]] || return 1
            echo grant >> "$TEST_DIR/operations"
            return ;;
        'ad app credential')
            [[ "$SCENARIO" != upload && "$4" == reset && " $* " == *' --append '* && " $* " == *' --display-name tenant-quota-'* ]] || return 99
            echo mint >> "$TEST_DIR/operations"
            echo new-password
            return ;;
        'keyvault secret show'|'keyvault secret set') ;;
        *) return 99 ;;
    esac
    local operation="$3" name="" file=""
    shift 3
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --name) name="$2"; shift 2 ;;
            --file) file="$2"; shift 2 ;;
            *) shift ;;
        esac
    done
    if [[ "$operation" == show ]]; then
        if [[ "$SCENARIO" == lookup-failure || ( "$SCENARIO" == readback-failure && -f "$TEST_DIR/published" ) ]]; then echo sensitive-error >&2; return 1; fi
        if [[ -f "$TEST_DIR/vault/$name" ]]; then cat "$TEST_DIR/vault/$name"; else echo SecretNotFound >&2; return 1; fi
    else
        if [[ "$SCENARIO" == write-failure && "$name" == *-client-id ]]; then echo sensitive-error >&2; return 1; fi
        cp "$file" "$TEST_DIR/vault/$name"
        touch "$TEST_DIR/published"
        echo publish >> "$TEST_DIR/operations"
    fi
}
curl() {
    if [[ "$SCENARIO" == token-failure ]]; then echo sensitive-error >&2; return 1; fi
    echo '{"access_token":"test-token"}'
}
export -f az curl yq
arguments=(--tenant test-tenant --config "$TEST_DIR/config.json" --keyvault test-vault)
case "$SCENARIO" in
    existing-handoff|deferred)
        arguments+=(--skip-keyvault --defer-secret-out "$TEST_DIR/handoff.json")
        if [[ "$SCENARIO" == existing-handoff ]]; then echo sentinel > "$TEST_DIR/handoff.json"; fi ;;
    staged) arguments+=(--secret-name next-client-secret) ;;
    direct) arguments+=(--keyvault-only --app-id 11111111-1111-1111-1111-111111111111 --secret-name test-client-secret) ;;
esac
if bash scripts/manage-service-principals.sh "${arguments[@]}"; then
    [[ "$SCENARIO" == success || "$SCENARIO" == new-sp || "$SCENARIO" == deferred || "$SCENARIO" == existing-pair || "$SCENARIO" == staged || "$SCENARIO" == direct ]]
else
    [[ "$SCENARIO" != success && "$SCENARIO" != new-sp && "$SCENARIO" != deferred && "$SCENARIO" != existing-pair && "$SCENARIO" != staged && "$SCENARIO" != direct ]]
fi
case "$SCENARIO" in
    wrong-tenant|existing-handoff|ambiguous-subscription)
        [[ ! -s "$TEST_DIR/operations" ]]
        if [[ "$SCENARIO" == existing-handoff ]]; then [[ "$(cat "$TEST_DIR/handoff.json")" == sentinel ]]; fi ;;
    grant-failure) [[ "$(cat "$TEST_DIR/operations")" == create ]] ;;
    lookup-failure|mismatched-binding|existing-pair) [[ "$(cat "$TEST_DIR/operations")" == $'grant\ngrant' ]] ;;
    success|new-sp|staged|direct)
        [[ "$(grep -c '^mint$' "$TEST_DIR/operations")" == 1 && "$(grep -c '^publish$' "$TEST_DIR/operations")" == 2 ]]
        [[ -z "$(find "$TEST_DIR" -name pair.json)" ]]
        if [[ "$SCENARIO" == staged ]]; then
            [[ "$(cat "$TEST_DIR/vault/test-client-secret")" == old-password && "$(cat "$TEST_DIR/vault/next-client-secret")" == new-password ]]
        fi ;;
    *)
        [[ "$(grep -c '^mint$' "$TEST_DIR/operations")" == 1 ]]
        handoff="$TEST_DIR/handoff.json"
        if [[ "$SCENARIO" != deferred ]]; then handoff=$(find "$TEST_DIR" -name pair.json); fi
        [[ -f "$handoff" && "$(stat -c %a "$handoff")" == 600 ]]
        SCENARIO=upload bash scripts/manage-service-principals.sh --keyvault-only --keyvault test-vault --secret-name test-client-secret --secret-file "$handoff"
        [[ "$(grep -c '^mint$' "$TEST_DIR/operations")" == 1 && "$(cat "$TEST_DIR/vault/test-client-secret")" == new-password ]]
        ;;
esac
`)
			command.Env = append(os.Environ(), "TEST_DIR="+t.TempDir(), "SCENARIO="+scenario)
			output, err := command.CombinedOutput()
			require.NoError(t, err, "%s", output)
			for _, sensitive := range []string{"old-password", "new-password", "test-token", "sensitive-error"} {
				require.NotContains(t, string(output), sensitive)
			}
		})
	}
}

func TestMetricsEndpoint(t *testing.T) {
	registry := prometheus.NewRegistry()
	privateMetric := prometheus.NewGauge(prometheus.GaugeOpts{Name: "tenant_quota_endpoint_test", Help: "Private registry test."})
	registry.MustRegister(privateMetric)
	privateMetric.Set(42)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	writer := cijoboutcomes.NewWriter(&config.Config{}, logger)
	writer.RegisterMetrics(registry)
	// A cancelled Start shuts down the real controller queue without Azure calls.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	writer.Start(ctx)

	queue := workqueue.NewTypedRateLimitingQueueWithConfig(
		workqueue.NewTypedItemExponentialFailureRateLimiter[string](time.Millisecond, time.Second),
		workqueue.TypedRateLimitingQueueConfig[string]{Name: t.Name()},
	)
	defer queue.ShutDown()
	queue.Add("build")
	item, shutdown := queue.Get()
	require.False(t, shutdown)
	queue.Done(item)
	queue.AddRateLimited(item)
	utils.PanicTotal.WithLabelValues(t.Name()).Inc()

	server := httptest.NewServer(newHTTPHandler(registry))
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/metrics")
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode, string(body))
	for _, metric := range []string{
		"tenant_quota_endpoint_test 42",
		`ci_job_outcomes_cache_size{cache="batches"} 0`,
		`workqueue_depth{name="ci-job-outcomes"}`,
		`workqueue_adds_total{name="TestMetricsEndpoint"}`,
		`workqueue_retries_total{name="TestMetricsEndpoint"}`,
		`workqueue_queue_duration_seconds_count{name="TestMetricsEndpoint"}`,
		`workqueue_work_duration_seconds_count{name="TestMetricsEndpoint"}`,
		`panic_total{controller="TestMetricsEndpoint"}`,
		"go_goroutines ",
	} {
		require.Contains(t, string(body), metric)
	}
}

func TestServeShutdown(t *testing.T) {
	for _, failHTTP := range []bool{false, true} {
		name := "cancellation"
		if failHTTP {
			name = "HTTP failure"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			controllerCancelled := make(chan struct{})
			controllerRelease := make(chan struct{})
			defer func() {
				select {
				case <-controllerRelease:
				default:
					close(controllerRelease)
				}
			}()
			startController := func(ctx context.Context) {
				<-ctx.Done()
				close(controllerCancelled)
				<-controllerRelease
			}
			address := make(chan string, 1)
			requestStarted := make(chan struct{})
			requestRelease := make(chan struct{})
			defer func() {
				select {
				case <-requestRelease:
				default:
					close(requestRelease)
				}
			}()
			server := &http.Server{
				Addr: "127.0.0.1:0",
				BaseContext: func(listener net.Listener) context.Context {
					address <- listener.Addr().String()
					return context.Background()
				},
				Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					close(requestStarted)
					<-requestRelease
					w.WriteHeader(http.StatusOK)
				}),
			}
			defer server.Close()
			if failHTTP {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, err)
				defer listener.Close()
				server.Addr = listener.Addr().String()
			}
			finished := make(chan error, 1)
			go func() { finished <- serve(ctx, cancel, logger, server, startController) }()
			if !failHTTP {
				var addr string
				select {
				case addr = <-address:
				case <-time.After(5 * time.Second):
					t.Fatal("HTTP server did not start")
				}
				responseDone := make(chan error, 1)
				go func() {
					response, err := http.Get("http://" + addr)
					if err == nil {
						response.Body.Close()
					}
					responseDone <- err
				}()
				select {
				case <-requestStarted:
				case <-time.After(5 * time.Second):
					t.Fatal("HTTP request did not start")
				}
				cancel()
				select {
				case <-controllerCancelled:
				case <-time.After(5 * time.Second):
					t.Fatal("controller was not cancelled alongside HTTP shutdown")
				}
				close(requestRelease)
				select {
				case err := <-responseDone:
					require.NoError(t, err, "in-flight HTTP request must finish gracefully")
				case <-time.After(5 * time.Second):
					t.Fatal("HTTP request did not finish")
				}
			}
			select {
			case <-controllerCancelled:
			case <-time.After(5 * time.Second):
				t.Fatal("controller was not cancelled")
			}
			select {
			case err := <-finished:
				t.Fatalf("serve returned before controller joined: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			close(controllerRelease)
			select {
			case err := <-finished:
				if failHTTP {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("serve did not join controller")
			}
		})
	}
}
