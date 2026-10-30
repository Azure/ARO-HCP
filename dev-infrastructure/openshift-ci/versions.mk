# Tool versions for the ARO-HCP openshift-ci images and integration helpers.
#
# This file is the single source of truth for tool versions used in the
# CI containers. Update versions here and run `make verify` to
# validate, then `make test` or `make test-nested-podman` to smoke-test the image.
#
# Upstream release pages:
#   Builder:   .ci-operator.yaml (build_root_image.tag)
#   promtool:  https://github.com/prometheus/prometheus/releases
#   bicep:     https://github.com/Azure/bicep/releases
#   gotestsum: https://github.com/gotestyourself/gotestsum/releases
#   envtest:   https://github.com/kubernetes-sigs/controller-tools/releases
#
# ENVTEST_VERSION pins the setup-envtest downloader independently of the runtime
# library. Update it and ENVTEST_K8S_VERSION deliberately for the Kubernetes
# versions covered by integration tests, rather than following upstream latest.

BUILDER_IMAGE_TAG ?= $(shell yq '.build_root_image.tag' ../../.ci-operator.yaml)
PROMTOOL_VERSION  ?= 3.15.1
BICEP_VERSION     ?= 0.48.1
GOTESTSUM_VERSION ?= 1.13.0
ENVTEST_VERSION   ?= v0.0.0-20260125163108-a19ec76a3c5d
ENVTEST_K8S_VERSION ?= 1.34.1
