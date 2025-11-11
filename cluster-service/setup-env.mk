SHELL = /bin/bash
SHELLFLAGS = -eu -o pipefail

ifndef zz_injected_EV2
ifndef RUNS_IN_TEMPLATIZE
SCRIPT_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
PROJECT_ROOT_DIR := $(realpath $(SCRIPT_DIR)/..)

DEPLOY_ENV ?= pers
PIPELINE ?= pipeline.yaml
PIPELINE_STEP ?= deploy
LOG_LEVEL ?= "0"
HASH = $(shell echo -n "$(DEPLOY_ENV)$(PIPELINE)$(PIPELINE_STEP)$(PWD)$(LOG_LEVEL)" | sha256sum | cut -d " " -f 1)
ENV_VARS_FILE ?= /tmp/deploy.${HASH}.cfg

# Target to generate the environment variables file
$(ENV_VARS_FILE): ${PROJECT_ROOT_DIR}/config/config.yaml ${PIPELINE} ${PROJECT_ROOT_DIR}/templatize.sh ${MAKEFILE_LIST}
	@echo "generate env vars file ${ENV_VARS_FILE}"
	@echo "this might take a while the first time."
	@LOG_LEVEL=${LOG_LEVEL} ${PROJECT_ROOT_DIR}/templatize.sh ${DEPLOY_ENV} \
		-p $(shell yq .serviceGroup ${PIPELINE}) \
		-s ${PIPELINE_STEP} \
		-o $(ENV_VARS_FILE)

# Include the environment variables file if it exists
-include ${ENV_VARS_FILE}
endif
endif
