SHELL = /bin/bash
SHELLFLAGS = -eu -o pipefail

ifndef zz_injected_EV2
ifndef RUNS_IN_TEMPLATIZE
PROJECT_ROOT_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))

DEPLOY_ENV ?= pers
ENV_MK_TMPL ?= Env.mk
LOG_LEVEL ?= "0"
HASH = $(shell echo -n "$(DEPLOY_ENV)$(ENV_MK_TMPL)$(PWD)$(LOG_LEVEL)" | sha256sum | cut -d " " -f 1)
ENV_VARS_FILE ?= /tmp/env.${HASH}.mk

# Target to generate the environment variables file
$(ENV_VARS_FILE): ${PROJECT_ROOT_DIR}/config/config.yaml ${ENV_MK_TMPL} ${PROJECT_ROOT_DIR}/templatize.sh ${MAKEFILE_LIST}
	@echo "generate env vars file ${ENV_VARS_FILE}"
	@echo "this might take a while the first time."
	@LOG_LEVEL=${LOG_LEVEL} ${PROJECT_ROOT_DIR}/templatize.sh ${DEPLOY_ENV} \
		${ENV_MK_TMPL} \
		${ENV_VARS_FILE}

-include ${ENV_VARS_FILE}
endif
endif
