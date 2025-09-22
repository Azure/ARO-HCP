include ../.bingo/Variables.mk

RMO_BUNDLE_VERSION ?= 0.1.852-g7f505c9
RMO_CHART_DIR ?= ${HELM_BASE_DIR}/route-monitor-operator
RMO_OPERATOR_BUNDLE_REPO ?= https://gitlab.cee.redhat.com/service/saas-route-monitor-operator-bundle
RMO_OPERATOR_BUNDLE_PATH ?= route-monitor-operator/${RMO_BUNDLE_VERSION}
RMO_OPERATOR_BUNDLE_BRANCH ?= production
HELM_BASE_DIR = deploy/helm

helm-chart:
	$(eval BUNDLE_TMP_PATH := $(shell mktemp -d tmp-rmo-bundle.XXX))
	git clone -b ${RMO_OPERATOR_BUNDLE_BRANCH} --depth=1 --single-branch -- ${RMO_OPERATOR_BUNDLE_REPO} ${BUNDLE_TMP_PATH}
	rm -rf ${RMO_CHART_DIR}
	@echo "generating chart from file://${BUNDLE_TMP_PATH}/${RMO_OPERATOR_BUNDLE_PATH}"
	go run ../tooling/olm-bundle-repkg/main.go \
		-b file://${BUNDLE_TMP_PATH}/${RMO_OPERATOR_BUNDLE_PATH} \
		-l ${RMO_OPERATOR_BUNDLE_REPO}/${RMO_OPERATOR_BUNDLE_PATH} \
		-c olm-bundle-repkg-config.yaml \
		-s scaffold \
		-o ${HELM_BASE_DIR} \
		--values-file-name values-generated.yaml
	rm -rf ${BUNDLE_TMP_PATH}
	# TMP Fix: Add runAsUser to route-monitor-operator-controller-manager Deployment
	yq eval '.spec.template.spec.containers[0].securityContext.runAsUser = 65532' \
		-i ${RMO_CHART_DIR}/templates/route-monitor-operator-controller-manager.deployment.yaml
	# Format only the generated chart files instead of the entire project
	find ${RMO_CHART_DIR} -name "*.yaml" -o -name "*.yml" | xargs -r $(YAMLFMT)
.PHONY: helm-chart

update-digests-in-config: $(YAMLFMT)
	$(eval OPERATOR_DIGEST := $(shell yq '.imageDigestOperator' ${RMO_CHART_DIR}/values-generated.yaml))
	$(eval BLACKBOX_DIGEST := $(shell yq '.imageDigestBlackbox' ${RMO_CHART_DIR}/values-generated.yaml))
	@echo "Updating config.yaml with new digests:"
	@echo "  Operator digest: ${OPERATOR_DIGEST}"
	@echo "  Blackbox digest: ${BLACKBOX_DIGEST}"
	make -s -C ../tooling/yamlwrap yamlwrap
	../tooling/yamlwrap/yamlwrap wrap --input ../config/config.yaml --no-validate-result
	yq eval '.defaults.routeMonitorOperator.operatorImage.digest = "${OPERATOR_DIGEST}"' -i ../config/config.yaml
	yq eval '.defaults.routeMonitorOperator.blackboxExporterImage.digest = "${BLACKBOX_DIGEST}"' -i ../config/config.yaml
	$(YAMLFMT) ../config/config.yaml
	../tooling/yamlwrap/yamlwrap unwrap --input ../config/config.yaml
.PHONY: update-digests-in-config
