# OpenShift CI Tooling

This directory holds the CI image definitions (`Dockerfile`,
`Dockerfile.nested-podman`), their Makefile, the pinned tool versions in
`versions.mk`, and the tool smoke tests. It contains no documentation of its own.

- [CI Image Lifecycle](../../docs/ci/image-lifecycle.md) covers the shared build
  root, how the tool versions are pinned and updated, and the nested Podman
  toolchain with its local build and test commands.
- [Cluster Profile Secret Contract](../../docs/ci/cluster-profile-secret-contract.md)
  defines the credentials these images consume at runtime.
- [Rotating CI Bot Credentials](../../docs/sops/rotate-ci-bot-credentials.md) and
  [Rotating the CI Client Secret](../../docs/sops/test-test-tenant-access.md#rotating-the-ci-client-secret)
  are the rotation procedures, one per tenant.

Keep this file a pointer. Procedures and explanations belong in the pages above
so that each one has a single copy.
