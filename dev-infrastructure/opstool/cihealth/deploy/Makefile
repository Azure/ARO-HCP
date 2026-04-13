CIHEALTH_DEPLOY_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
PROJECT_ROOT_DIR := $(abspath $(CIHEALTH_DEPLOY_DIR)/../../../..)

include $(PROJECT_ROOT_DIR)/tooling/templatize/Makefile

DEPLOY_ENV ?= opstool
CONFIG_FILE ?= $(PROJECT_ROOT_DIR)/config/config-opstool.yaml
TOPOLOGY_FILE ?= $(PROJECT_ROOT_DIR)/topology-opstool.yaml
DEV_SETTINGS_FILE ?= $(PROJECT_ROOT_DIR)/tooling/templatize/settings.yaml
SERVICE_GROUP ?= Microsoft.Azure.ARO.HCP.Opstool.CIHealth
STEP ?=

PIPELINE_RUN_ARGS = \
	--config-file "$(CONFIG_FILE)" \
	--topology-file "$(TOPOLOGY_FILE)" \
	--dev-settings-file "$(DEV_SETTINGS_FILE)" \
	--dev-environment "$(DEPLOY_ENV)" \
	--service-group "$(SERVICE_GROUP)"

ifneq ($(strip $(STEP)),)
PIPELINE_RUN_ARGS += --step "$(STEP)"
endif

deploy: $(TEMPLATIZE)
	$(TEMPLATIZE) pipeline run $(PIPELINE_RUN_ARGS)
.PHONY: deploy

dry-run: $(TEMPLATIZE)
	$(TEMPLATIZE) pipeline run --dry-run $(PIPELINE_RUN_ARGS)
.PHONY: dry-run
