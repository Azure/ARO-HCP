# Tool versions for the ARO-HCP openshift-ci image.
#
# This file is the single source of truth for tool versions used in the
# CI container. Update versions here and run `make verify-versions` to
# validate, then `make test` to confirm the image builds correctly.
#
# Upstream release pages:
#   Builder:   .ci-operator.yaml (build_root_image.tag)
#   promtool:  https://github.com/prometheus/prometheus/releases

BUILDER_IMAGE_TAG ?= $(shell yq '.build_root_image.tag' ../../.ci-operator.yaml)
PROMTOOL_VERSION  ?= 3.2.1
