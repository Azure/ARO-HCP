# Fixing OpenShift Version Mismatch in E2E Tests

## Problem Summary

**Error**: `Node pool version '4.20.20' must not be greater than Control Plane version '4.20.19'`

**Failing Tests**:
- Customer should update node pool labels and taints
- Customer should use workload identity via cluster OIDC
- Customer should not perform invalid operations

## Root Cause

The version mismatch occurs due to **timing differences in Cincinnati version resolution**:

1. **Control Plane Version**: Fetched once at test start (cached via `sync.Once`) → got `4.20.19`
2. **Node Pool Version**: Fetched separately (potentially later) → got `4.20.20`
3. Cincinnati released `4.20.20` between the two fetch operations

### Why This Happens

See `test/util/framework/deployment_params.go`:

- `resolveDefaultControlPlaneVersion()`: Caches first result
- `DefaultOpenshiftNodePoolVersionId()`: Should reuse CP version when channels match, but:
  - Race condition window exists between fetches
  - Explicit env vars can override and cause mismatches

## Solution

Code changes in `deployment_params.go`. Alternatively you may use the scripts provided for a quick workaround, explanation is below.

## 📋 Environment Variable Synchronization

#### For Local Development (Bash)

```bash
# Use the synchronization script
source test/scripts/set-ocp-versions.sh candidate 4.20
```

#### For Local Development (PowerShell - Windows)

```powershell
# Run the PowerShell script
.\test\scripts\Set-OcpVersions.ps1 -ChannelGroup "candidate" -VersionMinor "4.20"
```

#### For CI/CD (Prow, GitHub Actions)

Add this to your CI pipeline BEFORE running tests:

```bash
# In your Prow job or GitHub Actions workflow
source test/scripts/sync-ocp-versions-ci.sh
```

**Or manually set**:

```bash
export ARO_HCP_OPENSHIFT_CHANNEL_GROUP="candidate"
export ARO_HCP_OPENSHIFT_NODEPOOL_CHANNEL_GROUP="candidate"
export ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION="4.20.20"  # Use same version!
export ARO_HCP_OPENSHIFT_NODEPOOL_VERSION="4.20.20"      # Use same version!
```

## Scripts Provided

### 1. `test/scripts/check-channel-groups.sh`
**Purpose**: Diagnostic tool to check current configuration
**Usage**: `./test/scripts/check-channel-groups.sh`
**When**: Run when debugging version mismatch issues

### 2. `test/scripts/set-ocp-versions.sh`
**Purpose**: Set synchronized versions for local testing (Bash)
**Usage**: `source ./test/scripts/set-ocp-versions.sh [channel] [version]`
**When**: Before running tests locally on Linux/macOS

### 3. `test/scripts/Set-OcpVersions.ps1`
**Purpose**: Set synchronized versions for local testing (PowerShell)
**Usage**: `.\test\scripts\Set-OcpVersions.ps1 -ChannelGroup candidate -VersionMinor 4.20`
**When**: Before running tests locally on Windows

### 4. `test/scripts/sync-ocp-versions-ci.sh`
**Purpose**: CI/CD integration - fetches and synchronizes versions once
**Usage**: Add to CI pipeline: `source test/scripts/sync-ocp-versions-ci.sh`
**When**: Integrate into Prow jobs, GitHub Actions, etc.

## Important Notes

### Scripts Are NOT Auto-Executed

❗ **The scripts DO NOT run automatically during test execution.**

They are **helper tools** you can use:
- **Manually** before running tests locally
- **In CI/CD pipelines** (must be explicitly added to pipeline config)
- **For diagnostics** when troubleshooting version issues

### The Code Changes ARE Automatic

✅ **The code changes in `deployment_params.go` ARE automatic** - they execute as part of the normal test framework initialization.

## Recommended Approach

### For Immediate Fix (Quick)
1. Set environment variables explicitly:
   ```bash
   export ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION="4.20.20"
   export ARO_HCP_OPENSHIFT_NODEPOOL_VERSION="4.20.20"
   ```
2. Run your tests

### For Long-Term Fix (Best Practice)
1. The code changes are already applied ✅
2. Test to verify:
   ```bash
   # Build the test binary
   make -C test
   
   # Set required environment variables
   export CUSTOMER_SUBSCRIPTION=<subscriptionName>
   export LOCATION=uksouth
   
   # Run integration test suite
   ./test/aro-hcp-tests run-suite "integration/parallel" --junit-path="junit.xml"
   ```
3. For CI/CD: Add `sync-ocp-versions-ci.sh` to pipeline

## Validation Commands

### Check Configuration
```bash
./test/scripts/check-channel-groups.sh
```

### Verify Version Resolution
```bash
# List available test cases to see what's available
./test/aro-hcp-tests list | jq '.[].name'
```

### Run Failing Tests
```bash
# Ensure environment is configured
export CUSTOMER_SUBSCRIPTION=<subscriptionName>
export LOCATION=uksouth

# Run specific test cases
./test/aro-hcp-tests run-test "Customer should update node pool labels and taints"
./test/aro-hcp-tests run-test "Customer should use workload identity via cluster OIDC"
./test/aro-hcp-tests run-test "Customer should not perform invalid operations"
```

## Related Files Modified

1. ✅ `test/util/framework/deployment_params.go` - Core fix
2. ✅ `test/scripts/check-channel-groups.sh` - Diagnostic tool
3. ✅ `test/scripts/set-ocp-versions.sh` - Local dev helper (Bash)
4. ✅ `test/scripts/Set-OcpVersions.ps1` - Local dev helper (PowerShell)
5. ✅ `test/scripts/sync-ocp-versions-ci.sh` - CI/CD helper
