# Azure VNet Fluent Bit Regression Test

From the repository root, using a local Linux Fluent Bit binary:

```sh
FLUENT_BIT_BINARY=/path/to/fluent-bit go test ./tooling/helmtest/testrunner -run '^TestAzureVNetFluentBit$' -count=1 -v -timeout=3m
```

Or use the image currently pinned in `config/config.yaml` with Podman (set
`CONTAINER_RUNTIME=docker` to use Docker):

```sh
FLUENT_BIT_IMAGE=mcr.microsoft.com/oss/v2/fluent/fluent-bit@sha256:94cee54eb85d08891b179bd74be48632bf6e7968f046225abb6074b4655891d0 \
  go test ./tooling/helmtest/testrunner -run '^TestAzureVNetFluentBit$' -count=1 -v -timeout=3m
```

Without either environment variable the test skips, with no Fluent Bit or container
dependency. Pull the image before running if downloading it would exceed the test
timeout. Container execution requires a local Linux runtime with bind mounts and
signal forwarding; networking is disabled inside the container.

The existing Helm renderer renders the current chart, not golden fixtures. The
test extracts the `azure-vnet.logs` input/filter blocks and its referenced parser
from the rendered ConfigMap. Only input paths are relocated; production tail
timings, database settings and parsing/filtering rules remain unchanged. A minimal
local service and JSON stdout output replace Kubernetes and remote output plumbing.
All logs are synthetic and all state is in a temporary directory.

Assertions cover millisecond event timestamps, retained `ts` and structured fields,
malformed/non-JSON fallback, rejection of unparsed configuration payloads,
top-level `stdinData`/`args` removal, trusted metadata
overrides including `NODE_NAME` as `hostname`, active-path-only collection, rotation
with immediate replacement-file and late old-inode writes, and restart using the same cursor without replay or
skipping unread downtime lines. This does not test Kusto ingestion or Helm fixtures.
