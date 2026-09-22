# bicep golden fixtures

Committed ARM JSON compiled from `demo/bicep` and `test/e2e-setup/bicep`
with the pinned `bicep` binary (see `dev-infrastructure/openshift-ci/versions.mk`).
Compiler version and templateHash metadata is stripped so these files
only change when the compiled output actually changes.

`make verify` (via `verify-bicep-fixtures`) recompiles the same bicep
sources and diffs the result against this directory. A diff means the
pinned bicep version, or a bicep source change, altered the compiled
output and needs review.

After an intentional bicep source or version change, regenerate and
commit the new baseline:

```
hack/generate-bicep-golden.sh
```
