SHELL = /bin/bash
SHELLFLAGS = -eu -o pipefail

ifndef EV2
ifndef RUNS_IN_TEMPLATIZE
PROJECT_ROOT_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))

DEPLOY_ENV ?= personal-dev
PIPELINE ?= pipeline.yaml
PIPELINE_STEP ?= deploy
HASH = $(shell echo -n "$(DEPLOY_ENV)$(PIPELINE)$(PIPELINE_STEP)" | md5)
ENV_VARS_FILE ?= ${TMPDIR}/deploy.${HASH}.cfg

# Target to generate the environment variables file
$(ENV_VARS_FILE): ${PROJECT_ROOT_DIR}/config/config.yaml ${PIPELINE} ${PROJECT_ROOT_DIR}/templatize.sh ${MAKEFILE_LIST}
	@echo "generate env vars file ${ENV_VARS_FILE}"
	@${PROJECT_ROOT_DIR}/templatize.sh ${DEPLOY_ENV} \
		-p ${PIPELINE} \
		-s ${PIPELINE_STEP} > $(ENV_VARS_FILE)

# Include the environment variables file if it exists
-include ${ENV_VARS_FILE}
endif
endif