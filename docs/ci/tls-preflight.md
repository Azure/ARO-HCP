# TLS Preflight (`tls-preflight`)

A presubmit Prow job that gates image-bump PRs carrying a kubectl we execute
at deploy time. It makes the kubectl inside the candidate image perform one
real TLS 1.3 handshake, so a runtime image whose kubectl panics on TLS 1.3
fails on the bump PR itself instead of days later as e2e-parallel provision
failures.

## Why this job exists

The `mcr.microsoft.com/aks/command/runtime` image (config component
`aksCommandRuntime`) ships the kubectl that several deployment Jobs execute
directly:

- `unannotate-default-sc` Job (mgmt-fixes/default-sc, management clusters)
- `finalize-mce` Job (acm/multicluster-engine, service clusters)
- the velero install Jobs
- the arobit cert-watchdog sidecar (observability/arobit forwarder
  DaemonSet; only invokes kubectl during the AROSLSRE-879 cert-mount
  recovery, so a broken kubectl there produces no deploy-time signal at all)

That image has shipped a broken kubectl **twice in six weeks**
(AROSLSRE-2021 in September, AROSLSRE-2481 in October). Both times the
defect was a panic during any TLS 1.3 handshake:

```text
panic: failed to add parameter data : OSSL_PARAM_BLD_push_octet_string
```

originating in the Go/OpenSSL FIPS integration of the Microsoft Go build
embedded in the image (`/usr/lib/msft-golang`). The only symptom in CI was
post-install hook Jobs failing with `Job Failed. failed: 1/1`, burning
45-minute e2e-parallel runs and requiring oncall forensics to trace back to
the image. The AROSLSRE-2476 investigation defined the acceptance criterion
for a rebuilt runtime: the kubectl must pass a TLS 1.3 check before a new
digest is selected. This job automates that check (AROSLSRE-2483).

## How it works

The job lives in the `podman` variant of the ci-operator config in
`openshift/release` and runs the [`aro-hcp-tls-preflight`] step, which
executes [`hack/ci/tls-preflight.sh`] from this repository:

1. **Diff gate**: in presubmits, the script compares each listed
   component's `defaults.<component>.image.digest` between `PULL_BASE_SHA`
   and the PR head. Components whose digest did not change are skipped, so
   unrelated `config/config.yaml` edits cost nothing. Without a usable base
   (periodics, rehearsals, local runs) every listed component is checked.
2. **Local TLS-1.3-only server**: the script starts an
   `openssl s_server -tls1_3` bound to loopback. The server refuses every
   other protocol version, so any completed handshake necessarily exercised
   kubectl's TLS 1.3 code path. No external endpoint or credential is
   involved, and the runner-side `openssl s_client` probe proves the
   listener is up before kubectl is blamed.
3. **The check**: `podman run --network host --entrypoint kubectl
   <registry/repository@digest> --server=https://127.0.0.1:8443
   --insecure-skip-tls-verify --token=tls-preflight get --raw /`. kubectl
   must run **inside the image**: the binary dynamically links the image's
   own OpenSSL (`/usr/lib/msft-golang`), so extracting it and running it on
   the runner does not reproduce the failure. `curl`/`wget` checks would
   also miss it - they use their own TLS stack, not kubectl's.
4. **Verdicts** (fail closed):
   - kubectl panic / `OSSL_PARAM` output -> **fail** immediately, with the
     AROSLSRE-2021/2481 remediation hint
   - kubectl exit 0 **and** the local server's HTML session report showing
     `New, TLSv1.3, Cipher is ...` -> **pass**. Exit 0 alone, generic error
     prefixes, or HTTP-looking errors without that response cannot pass.
   - container-runtime failure (image pull, exit 125/126/127) -> knows
     nothing about kubectl, never reported as a pass; retried
   - connection-level or otherwise unverified results -> retried; still
     inconclusive after 3 attempts -> **fail**, so an unverified digest
     cannot merge

The pass condition requires the TLS 1.3-only server's session report in
kubectl's output, not merely a successful kubectl exit code.

## What it covers

- `aksCommandRuntime` today.
- The first-party kubectl image from AROSLSRE-2528 will be covered by
  adding its config component name to `TLS_PREFLIGHT_COMPONENTS` in the
  `aro-hcp-tls-preflight` step (openshift/release). Add the component to
  `config/config.yaml` first, then extend the step env - a listed component
  that is absent from config fails the job on purpose (fail closed) rather
  than silently not checking anything.
- Images must ship kubectl resolvable as the container entrypoint and be
  publicly pullable. The ARO-HCP ACR-hosted first-party image will need
  registry credentials added to the step before it can be checked.

## Running it

- On a PR: automatic when `config/config.yaml` changes
  (`run_if_changed`); manual via `/test podman-tls-preflight` (the job lives
  in the `podman` ci-operator variant, so its trigger command carries the
  variant prefix).
- Locally, from the repo root (requires podman or docker, openssl, git):

  ```bash
  ./hack/ci/tls-preflight.sh
  ```

- Against a specific digest without editing config, run with a modified
  config copy:

  ```bash
  sed 's|sha256:<old>|sha256:<new>|' config/config.yaml > /tmp/c.yaml
  TLS_PREFLIGHT_CONFIG=/tmp/c.yaml ./hack/ci/tls-preflight.sh
  ```

- Offline tests for the parser and verdict classification (no network, no
  containers, no openssl):

  ```bash
  ./hack/ci/tls-preflight-test.sh
  ```

## Interpreting failures

| Failed check | Meaning | Action |
|---|---|---|
| panic / `OSSL_PARAM` | The candidate kubectl has the AROSLSRE-2021/2481 regression class | Do not merge the digest; hold the bump until the runtime is rebuilt with a corrected kubectl (see AROSLSRE-2476) and passes this check |
| `could not verify ... failing closed` | The image could not be verified (repeated connection-level or runtime failures) | Retry the job once; if it persists, run the script locally against the digest to disambiguate a CI-network issue from a bad image |
| `component ... has no defaults...` | `TLS_PREFLIGHT_COMPONENTS` lists a component missing from `config/config.yaml` | Fix the ordering: land the config component first, then extend the step env |

## Known limitations

- The image's native platform is tested (the image is multi-arch; OpenShift
  CI may schedule the job on arm64 nodes, where the arm64 variant is
  checked). The guarded regression class is a Go/OpenSSL integration bug
  built into every architecture of the image, but per-arch regressions in
  principle could slip through on the non-native variant.
- The job validates the kubectl TLS 1.3 handshake only; nothing else about
  the image is checked.
- The job requires no secrets today. Covering an ACR-hosted first-party
  image (AROSLSRE-2528) will add a registry-credential requirement to the
  step.

[`hack/ci/tls-preflight.sh`]: ../../hack/ci/tls-preflight.sh
[`aro-hcp-tls-preflight`]: https://github.com/openshift/release/tree/master/ci-operator/step-registry/aro-hcp/tls-preflight

## Related documentation

- [CI Overview](README.md)
- [CI Execution](execution.md)
- [CI Image Lifecycle](image-lifecycle.md)
- AROSLSRE-2483 (this automation), AROSLSRE-2481 / AROSLSRE-2021 (the
  incidents), AROSLSRE-2476 (root cause and acceptance criterion),
  AROSLSRE-2528 (first-party kubectl image this check will also cover)
