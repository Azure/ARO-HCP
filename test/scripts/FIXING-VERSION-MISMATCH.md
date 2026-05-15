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

- `resolveDefaultControlPlaneVersion()` (line 95-113): Caches first result
- `DefaultOpenshiftNodePoolVersionId()` (line 135-163): Should reuse CP version when channels match, but:
  - Race condition window exists between fetches
  - Explicit env vars can override and cause mismatches

## Solutions

### ✅ SOLUTION 1: Code Changes (RECOMMENDED - ALREADY APPLIED)

**What Changed**:

1. **Enhanced synchronization logic** (`deployment_params.go:135-163`)
   - Now ALWAYS uses control plane version when channel groups match
   - Fixed edge case where versions could diverge
   - Added clear comments explaining the critical synchronization

2. **Early validation** (`deployment_params.go:213-235`)
   - Added version comparison in `NewDefaultNodePoolParams()`
   - Fails fast with helpful error message if mismatch detected
   - Shows exact configuration causing the issue

3. **Import added**: `github.com/blang/semver/v4` for version comparison

**Benefits**:
- ✅ Automatic - no manual steps needed
- ✅ Catches configuration errors early
- ✅ Works for all test runs (local, CI/CD)
- ✅ Prevents the issue at the source

**Action Required**: Test the changes

```bash
# Run a quick test to verify the fix
cd test
go test -v -run "TestNodePoolLabels" ./e2e/
```

### 📋 SOLUTION 2: Environment Variable Synchronization (ALTERNATIVE)

If you prefer explicit version control or need a quick workaround:

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
   cd test
   go test -v ./util/framework/ -run TestDefault
   ```
3. For CI/CD: Add `sync-ocp-versions-ci.sh` to pipeline

## Validation Commands

### Check Configuration
```bash
./test/scripts/check-channel-groups.sh
```

### Verify Version Resolution
```bash
# See what versions would be used
cd test/util/framework
go run -tags E2Etests . -ginkgo.dry-run
```

### Run Failing Tests
```bash
cd test
go test -v ./e2e/ -run "nodepool_labels_taints"
go test -v ./e2e/ -run "oidc_issuer_workload_identity"
go test -v ./e2e/ -run "simple_negative_cases"
```

## Related Files Modified

1. ✅ `test/util/framework/deployment_params.go` - Core fix
2. ✅ `test/scripts/check-channel-groups.sh` - Diagnostic tool
3. ✅ `test/scripts/set-ocp-versions.sh` - Local dev helper (Bash)
4. ✅ `test/scripts/Set-OcpVersions.ps1` - Local dev helper (PowerShell)
5. ✅ `test/scripts/sync-ocp-versions-ci.sh` - CI/CD helper