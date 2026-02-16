-include ../setup-templatize-env.mk
-include ../.bingo/Variables.mk

# Images & Registry
ACM_OPERATOR_BUNDLE_IMAGE ?= ${ACM_BUNDLE_REGISTRY}/${ACM_BUNDLE_REPOSITORY}@${ACM_BUNDLE_DIGEST}
MCE_OPERATOR_BUNDLE_IMAGE ?= ${MCE_BUNDLE_REGISTRY}/${MCE_BUNDLE_REPOSITORY}@${MCE_BUNDLE_DIGEST}
ARO_HCP_IMAGE_ACR ?= arohcpsvcdev
REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io

# Directories
HELM_BASE_DIR = deploy/helm
MCE_CONFIG_DIR = ${HELM_BASE_DIR}/multicluster-engine-config
BUILD_DIR = _build
MCE_OPERATOR_BUNDLE_FILE = $(BUILD_DIR)/mce-operator-bundle.tgz
OVERRIDE_CONFIG_FILE := /tmp/acm-override-config-$(shell date +%s).yaml

# Tools
SKOPEO ?= $(shell which skopeo 2>/dev/null)
YQ ?= yq
HELM ?= helm

export ACM_OPERATOR_BUNDLE_IMAGE
export REGISTRY
export DRY_RUN ?= false

ACM_VERSION := $(shell skopeo inspect docker://${ACM_OPERATOR_BUNDLE_IMAGE} --no-tags | jq -r '.Labels.version // empty')
MCE_VERSION := $(shell skopeo inspect docker://${MCE_OPERATOR_BUNDLE_IMAGE} --no-tags | jq -r '.Labels.version // empty')

ifeq ($(ACM_VERSION),)
	ACM_VERSION := 0.0.0-unknown
endif
ifeq ($(MCE_VERSION),)
	MCE_VERSION := 0.0.0-unknown
endif

.PHONY: debug-versions
debug-versions:
	@echo "ACM Version: $(ACM_VERSION)"
	@echo "MCE Version: $(MCE_VERSION)"

# Explicit clean target
.PHONY: clean
clean:
	rm -rf $(BUILD_DIR)

# Initialize build
.PHONY: init-build
init-build: clean
	mkdir -p $(BUILD_DIR)

.PHONY: mce-helm-chart
mce-helm-chart:
	@mkdir -p $(BUILD_DIR)
ifeq ($(SKOPEO),)
	@echo "Please install skopeo" && exit 1
endif
	@echo "--- 1. Downloading Bundle Image ---"
	$(SKOPEO) copy --override-arch amd64 docker://${MCE_OPERATOR_BUNDLE_IMAGE} docker-archive:${MCE_OPERATOR_BUNDLE_FILE}
	
	@echo "--- 2. Generating Charts from Bundle ---"
	go run ../tooling/olm-bundle-repkg/main.go \
		--crd-chart-name multicluster-engine-crds \
		-c olm-bundle-repkg-config.yaml \
		-b oci://${MCE_OPERATOR_BUNDLE_FILE} \
		-o ${BUILD_DIR} -s scaffold \
		-l ${MCE_OPERATOR_BUNDLE_IMAGE}
	
	@echo "--- 3. Packaging MCE Charts (Version: $(MCE_VERSION)) ---"
	$(HELM) package ${BUILD_DIR}/multicluster-engine \
		--version $(MCE_VERSION) --app-version $(MCE_VERSION) --destination ${BUILD_DIR}
	$(HELM) package ${BUILD_DIR}/multicluster-engine-crds \
		--version $(MCE_VERSION) --app-version $(MCE_VERSION) --destination ${BUILD_DIR}

.PHONY: policy-helm-chart
policy-helm-chart:
	@mkdir -p $(BUILD_DIR)
	@echo "--- Packaging Policy/Config Chart (Version: $(ACM_VERSION)) ---"
	cp -r ${MCE_CONFIG_DIR} ${BUILD_DIR}/multicluster-engine-config
	mkdir -p ${BUILD_DIR}/multicluster-engine-config/charts/policy/charts/grc/templates
	mkdir -p ${BUILD_DIR}/multicluster-engine-config/charts/policy/charts/cluster-lifecycle/templates
	mkdir -p ${BUILD_DIR}/multicluster-engine-config/charts/policy/crds

	./update-policy-chart.sh "${BUILD_DIR}/multicluster-engine-config/charts/policy"

	$(HELM) package ${BUILD_DIR}/multicluster-engine-config \
		--version $(ACM_VERSION) \
		--app-version $(ACM_VERSION) \
		--destination ${BUILD_DIR}

.PHONY: helm-charts
helm-charts: init-build
	$(MAKE) mce-helm-chart
	$(MAKE) policy-helm-chart
	@echo "Build complete. Artifacts in $(BUILD_DIR)/"


.PHONY: push-charts
push-charts:
    # OCI Image SHA will change every time they're built even if the charts haven't changed.  Checking to see if image needs updated before pushing prevents endless loop with image-updater
	@echo "Checking registry for existing charts..."

	# 1. MCE Chart
	@REPO="${REGISTRY}/helm/multicluster-engine"; \
	TAG="$(MCE_VERSION)"; \
	PKG="$(BUILD_DIR)/multicluster-engine-$$TAG.tgz"; \
	if $(SKOPEO) inspect --raw "docker://$$REPO:$$TAG" >/dev/null 2>&1; then \
		echo "MCE Chart $$TAG already exists. Fetching Digest..."; \
		$(SKOPEO) inspect --raw "docker://$$REPO:$$TAG" | sha256sum | awk '{print "sha256:"$$1}' > $(BUILD_DIR)/mce_digest; \
	else \
		echo "Pushing $$PKG..."; \
		$(HELM) push $$PKG "oci://${REGISTRY}/helm" $(HELM_OPTS) 2>&1 | tee /dev/tty | grep "Digest:" | awk '{print $$2}' | tr -d '\r' > $(BUILD_DIR)/mce_digest; \
	fi


	# 2. CRDs Chart
	@REPO="${REGISTRY}/helm/multicluster-engine-crds"; \
	TAG="$(MCE_VERSION)"; \
	PKG="$(BUILD_DIR)/multicluster-engine-crds-$$TAG.tgz"; \
	if $(SKOPEO) inspect --raw "docker://$$REPO:$$TAG" >/dev/null 2>&1; then \
		echo "RDs Chart $$TAG already exists. Fetching Digest..."; \
		$(SKOPEO) inspect --raw "docker://$$REPO:$$TAG" | sha256sum | awk '{print "sha256:"$$1}' > $(BUILD_DIR)/crds_digest; \
	else \
		echo "Pushing $$PKG..."; \
		$(HELM) push $$PKG "oci://${REGISTRY}/helm" $(HELM_OPTS) 2>&1 | tee /dev/tty | grep "Digest:" | awk '{print $$2}' | tr -d '\r' > $(BUILD_DIR)/crds_digest; \
	fi

	# 3. Config Chart
	@REPO="${REGISTRY}/helm/multicluster-engine-local-cluster"; \
	TAG="$(ACM_VERSION)"; \
	PKG="$(BUILD_DIR)/multicluster-engine-local-cluster-$$TAG.tgz"; \
	if $(SKOPEO) inspect --raw "docker://$$REPO:$$TAG" >/dev/null 2>&1; then \
		echo "onfig Chart $$TAG already exists. Fetching Digest..."; \
		$(SKOPEO) inspect --raw "docker://$$REPO:$$TAG" | sha256sum | awk '{print "sha256:"$$1}' > $(BUILD_DIR)/config_digest; \
	else \
		echo "Pushing $$PKG..."; \
		$(HELM) push $$PKG "oci://${REGISTRY}/helm" $(HELM_OPTS) 2>&1 | tee /dev/tty | grep "Digest:" | awk '{print $$2}' | tr -d '\r' > $(BUILD_DIR)/config_digest; \
	fi
	$(MAKE) clean

.PHONY: build-and-push
build-and-push: 
	az acr login --name ${ARO_HCP_IMAGE_ACR} --expose-token --output tsv --query accessToken | \
		$(HELM) registry login $(REGISTRY) --username 00000000-0000-0000-0000-000000000000 --password-stdin
	$(MAKE) helm-charts
	$(MAKE) push-charts

.PHONY: record-override deploy
record-override: $(YQ)
	@echo "Generating Override Config..."
	$(eval MCE_SHA := $(shell cat $(BUILD_DIR)/mce_digest))
	$(eval CRDS_SHA := $(shell cat $(BUILD_DIR)/crds_digest))
	$(eval CFG_SHA := $(shell cat $(BUILD_DIR)/config_digest))
	$(YQ) eval -n "\
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.acm.chartMce.digest = \"$(MCE_SHA)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.acm.chartConfig.digest = \"$(CFG_SHA)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.acm.chartCrds.digest = \"$(CRDS_SHA)\" \
	" > $(OVERRIDE_CONFIG_FILE)

deploy: build-and-push record-override
	$(MAKE) -C .. pipeline/ACM OVERRIDE_CONFIG_FILE=$(OVERRIDE_CONFIG_FILE)
	@rm -f $(OVERRIDE_CONFIG_FILE)
	$(MAKE) clean
