---
name: e2e-create-cluster
description: Build the E2E test binary, export required env vars, and run a simple cluster creation test against the dev environment. The cluster is kept running (skip cleanup).
---

# E2E Create Cluster (Dev Environment)

Run a simple E2E cluster creation test against the local development environment. The cluster is kept running after the test completes.

## Prerequisites

- `az login` must have been run and the correct subscription must be accessible.
- Port-forwarding to the dev RP must be set up (required for `AROHCP_ENV=development`).
- The test binary must be buildable (`make -C test`).

## Steps

1. **Build the test binary** (skip if already built and up to date):

```bash
make -C test
```

2. **Run the test** with the required environment variables and skip-cleanup:

```bash
export CUSTOMER_SUBSCRIPTION="ARO Hosted Control Planes (EA Subscription 1)"
export LOCATION="westus3"
export AROHCP_ENV="development"
export ARO_E2E_SKIP_CLEANUP=true
export ARO_HCP_OPENSHIFT_CHANNEL_GROUP="stable"
export ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION="4.20"

./test/aro-hcp-tests run-test "ARO-HCP HyperShift Presubmit should create a cluster and nodepool to completion"
```

3. **Report the result** to the user, including:
   - Whether the cluster was created successfully.
   - The resource group name and cluster name (visible in the test output).
   - Remind the user that `ARO_E2E_SKIP_CLEANUP=true` was set, so the cluster remains running and must be manually deleted later.

## Optional overrides

The user may provide overrides when invoking the skill:
- A different test name (use `./test/aro-hcp-tests list | jq '.[].name'` to find available tests).
- A different location (override `LOCATION`).
- A different OpenShift version (set `ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION`).
- `ARO_E2E_SKIP_CLEANUP=false` if they want auto-cleanup.

Apply any overrides the user specifies; otherwise use the defaults above.
