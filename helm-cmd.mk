SHELL = /bin/bash
PROJECT_ROOT_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))

ifdef DRY_RUN
HELM_CMD ?= helm diff upgrade --install --dry-run=server --suppress-secrets --three-way-merge
else
HELM_CMD ?= ${PROJECT_ROOT_DIR}/hack/helm.sh
endif
