TEST_DIR := $(dir $(lastword $(MAKEFILE_LIST)))

SHELL = /bin/bash

all: build

BICEP_OUTPUT_DIR=$(TEST_DIR)e2e/test-artifacts/generated-test-artifacts/

DEMO_BICEP_DIR=$(TEST_DIR)../demo/bicep
DEMO_BICEP_SOURCES = $(shell find $(DEMO_BICEP_DIR) -name '*.bicep')
DEMO_BICEP_OUTPUTS = $(addprefix $(BICEP_OUTPUT_DIR)standard-cluster-create/,$(addsuffix .json,$(basename $(notdir $(DEMO_BICEP_SOURCES)))))
$(BICEP_OUTPUT_DIR)standard-cluster-create/%.json: $(DEMO_BICEP_DIR)/%.bicep
	@mkdir -p $(dir $@)
	az bicep build --file=$< --outfile=$@

TEST_BICEP_DIR=$(TEST_DIR)e2e-setup/bicep
TEST_BICEP_SOURCES = $(shell cd $(TEST_BICEP_DIR) && find * -name '*.bicep')
TEST_BICEP_OUTPUTS = $(addprefix $(BICEP_OUTPUT_DIR),$(addsuffix .json,$(basename $(TEST_BICEP_SOURCES))))
$(BICEP_OUTPUT_DIR)%.json: $(TEST_BICEP_DIR)/%.bicep
	@mkdir -p $(dir $@)
	az bicep build --file=$< --outfile=$@

update-go-fixtures:
	UPDATE=true go test --count=1 ./... -v
.PHONY: update-go-fixtures

update: $(DEMO_BICEP_OUTPUTS) $(TEST_BICEP_OUTPUTS) update-go-fixtures
.PHONY: update

TEST_SOURCES = $(shell find $(TEST_DIR) -name '*.go' -o -name '*.json' -o -name '*.bicep')
ARO_HCP_TESTS = $(TEST_DIR)aro-hcp-tests
$(ARO_HCP_TESTS): $(TEST_SOURCES) $(MAKEFILE_LIST) $(TEST_DIR)/go.mod $(TEST_DIR)/go.sum $(DEMO_BICEP_OUTPUTS) $(TEST_BICEP_OUTPUTS)
	cd $(TEST_DIR) && go build -o $@ ./cmd/aro-hcp-tests

PROW_JOB_EXECUTOR_SOURCES = $(shell find $(TEST_DIR) -name '*.go')
PROW_JOB_EXECUTOR = $(TEST_DIR)prow-job-executor
$(PROW_JOB_EXECUTOR): $(PROW_JOB_EXECUTOR_SOURCES) $(MAKEFILE_LIST) $(TEST_DIR)/go.mod $(TEST_DIR)/go.sum
	cd $(TEST_DIR) && go build -o $@ ./cmd/prow-job-executor

build: $(PROW_JOB_EXECUTOR) $(ARO_HCP_TESTS)
.PHONY: build

int-e2e:
	PROW_JOB_NAME="$$(yq .clouds.public.environments.int.defaults.e2e.regionTest.prowJobName < ../config/config.msft.clouds-overlay.yaml)" \
	REGION="uksouth" \
	$(MAKE) e2e

stage-e2e:
	PROW_JOB_NAME="$$(yq .clouds.public.environments.stg.defaults.e2e.regionTest.prowJobName < ../config/config.msft.clouds-overlay.yaml)" \
	REGION="uksouth" \
	$(MAKE) e2e

prod-e2e:
	PROW_JOB_NAME="$$(yq .clouds.public.environments.prod.defaults.e2e.regionTest.prowJobName < ../config/config.msft.clouds-overlay.yaml)" \
	$(MAKE) e2e

e2e: $(PROW_JOB_EXECUTOR)
	PROW_JOB_EXECUTOR=$(PROW_JOB_EXECUTOR) \
	PROW_TOKEN_KEYVAULT="$$(yq .global.keyVault.name < ../config/rendered/dev/dev/westus3.yaml)" \
	PROW_TOKEN_SECRET="$$(yq .e2e.prow.globalKeyVaultTokenSecret < ../config/rendered/dev/dev/westus3.yaml)" \
	KEY_VAULT_DNSSUFFIX="vault.azure.net" \
	DRY_RUN="false" \
	GATE_PROMOTION="true" \
	./execute-prow-job.sh
