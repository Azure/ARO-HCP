SHELL = /bin/bash

build: update
	go build github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests
.PHONY: build

prow-job-executor:
	go build -o $@ ./cmd/prow-job-executor
.PHONY: prow-job-executor

update: update-bicep-json
.PHONY: update

update-bicep-json:
	hack/update-bicep-json.sh
.PHONY: -bicep-json

verify: verify-bicep-json
.PHONY: update

verify-bicep-json:
	hack/verify-bicep-json.sh
.PHONY: -bicep-json

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

e2e: prow-job-executor
	PROW_TOKEN_KEYVAULT="$$(yq .global.keyVault.name < ../config/rendered/dev/dev/westus3.yaml)" \
	PROW_TOKEN_SECRET="$$(yq .e2e.prow.globalKeyVaultTokenSecret < ../config/rendered/dev/dev/westus3.yaml)" \
	KEY_VAULT_DNSSUFFIX="vault.azure.net" \
	DRY_RUN="false" \
	GATE_PROMOTION="true" \
	./execute-prow-job.sh
