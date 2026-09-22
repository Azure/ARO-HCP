# Linux Binary Integration Test

Run from the repository root:

```sh
SWIFT_RECORDER_TEST_INTEGRATION=1 go test ./swift-recorder -run '^TestRecorderIntegration$' -count=1 -v -timeout=3m
```

Ordinary `go test` skips this heavy test. It is Linux-only and requires a Go
toolchain, enabled unprivileged user namespaces, and permission to create mount
and network namespaces, mount tmpfs/nsfs, use `setns`, and read rtnetlink. The
capture helper also requires kernel `openat2` support. No sudo, Kubernetes
cluster, Azure login, container runtime, or external `ip`/`unshare` tools are
needed. Once opted in, missing kernel or sandbox permissions are failures, not
skips. A container's seccomp/AppArmor policy may prohibit these operations even
when the host supports them; run on an appropriately configured Linux test host
rather than granting this test host-level privileges.

## Isolation

The parent builds the real recorder binary with an explicit output path in a
temporary directory under `/tmp`, never in the worktree. It re-executes the test
in fresh user, mount, and network namespaces, mapping only the caller's UID/GID
to root. `CAP_SYS_ADMIN` and `CAP_NET_ADMIN` apply inside that user namespace,
not the host's user namespace. Mount propagation is made recursively private
before mounting a temporary tmpfs for all fixtures and recorder logs.

Only the isolated HTTP network's loopback is brought up. A second fresh network
namespace, with its loopback still down, is bind-mounted from
`/proc/thread-self/ns/net` at `netns/cni-integration`. The controller runs in the
HTTP network, and its real `capture` subprocess must enter the second namespace.
The test checks the namespace device/inode against the bind mount, distinguishes
it from both the controller and parent namespaces, and checks the down loopback.
It never mounts under host `/run/netns` or reads/appends a host Azure VNet log.

The recorder receives an explicit kubeconfig pointing only at the fake local
HTTP server. That server accepts only node-filtered Pod GET list/watch requests;
all other requests fail the test. No real cluster API is contacted or mutated.
The fixture deliberately rejects `sendInitialEvents` as unsupported, exercising
client-go's fallback from streaming lists to conventional HTTP list/watch.
The recorder is terminated and reaped, with bounded shutdown and process-group
cleanup on timeout. The parent verifies the private tmpfs fixtures did not
escape the mount namespace, then removes its temporary build directory.

## Assertions

- Start the compiled binary in `controller --capture-mode=all`, using synthetic
  boot ID, kubeconfig, CNI log, and namespace directory inputs.
- Wait for `/readyz` and an established HTTP Pod watch before publishing a fresh,
  node-assigned Pod requesting `aro.openshift.io/swift-nic` and appending a
  realistic JSON Azure VNet `Processing ADD command` record.
- Require a published snapshot from the real helper subprocess, with matching
  Pod UID, sandbox ID, ADD timestamps, namespace path and device/inode identity,
  valid capture timestamps, all five nontruncated netlink sections, and only the
  isolated capture namespace's down loopback.
- Publish a watch update with `PodReadyToStartContainers=True` and `PodReady=True`.
  The former is the recorder's network-success signal; `PodReady` alone is not.
  Require exactly one open, recovery and `close` with reason `recovered`, in that
  lifecycle, plus at least one successful snapshot. Error-only output cannot pass.
- Check `/healthz`, successful capture and episode counters, and the standard
  `workqueue_*` metric samples labeled `name="swift-startup-recorder"`.
- Require clean SIGTERM shutdown. Recorder output is included on failure.

An initial `attempt_unavailable` observation is allowed because informer delivery
can precede CNI tailing. No helper failure, permission error, timeout or other
capture-error record is accepted. This tests the local binary wiring and real
Linux capture, not AKS CNI behavior, SWIFT hardware, cluster authorization, Helm
deployment, or log ingestion.
