SHELL = /bin/bash
SHELLFLAGS = -eu -o pipefail

PROJECT_ROOT_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))

include $(PROJECT_ROOT_DIR)tooling/templatize/Makefile

CONFIG_FILE := $(PROJECT_ROOT_DIR)config/config.yaml
DEV_SETTINGS_FILE := $(PROJECT_ROOT_DIR)tooling/templatize/settings.yaml

DEPLOY_ENV ?= pers
ENV_MK_TMPL ?= Env.mk
LOG_LEVEL ?= "0"
HASH = $(shell echo -n "$(DEPLOY_ENV)$(abspath $(ENV_MK_TMPL))$(LOG_LEVEL)" | sha256sum | cut -d " " -f 1)
ENV_VARS_FILE ?= /tmp/env.$(HASH).mk

# Target to generate the environment variables file
$(ENV_VARS_FILE): $(TEMPLATIZE) $(CONFIG_FILE) $(DEV_SETTINGS_FILE) $(ENV_MK_TMPL) $(MAKEFILE_LIST)
	@echo "generate env vars file $(ENV_VARS_FILE)"
	@echo "this might take a while the first time."
	$(TEMPLATIZE) generate \
		--config-file $(CONFIG_FILE) \
		--dev-settings-file $(DEV_SETTINGS_FILE) \
		--dev-environment $(DEPLOY_ENV) \
		--input $(ENV_MK_TMPL) \
		--output $(ENV_VARS_FILE) \
		-v $(LOG_LEVEL)

-include $(ENV_VARS_FILE)
