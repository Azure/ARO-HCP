include ../.bingo/Variables.mk

VELERO_VERSION := $(shell yq '.dependencies[] | select(.name == "velero") | .version' deploy/Chart.yaml)
VELERO_CHART_TGZ = deploy/charts/velero-$(VELERO_VERSION).tgz
VELERO_CRDS_DIR = deploy/crds

# Extract CRDs from the velero helm chart tgz
# The chart contains CRDs at velero/crds/*.yaml
extract-crds:
	rm -rf $(VELERO_CRDS_DIR)
	mkdir -p $(VELERO_CRDS_DIR)
	tar -xzf $(VELERO_CHART_TGZ) --strip-components=2 -C $(VELERO_CRDS_DIR) velero/crds
	make -C ../ yamlfmt

# Verify CRDs are in sync with the chart version
verify-crds:
	@if [ ! -f "$(VELERO_CHART_TGZ)" ]; then \
		echo "ERROR: Chart file $(VELERO_CHART_TGZ) not found. Run 'helm dependency update deploy/' first."; \
		exit 1; \
	fi
	@TMPDIR=$$(mktemp -d) && \
	tar -xzf $(VELERO_CHART_TGZ) --strip-components=2 -C $$TMPDIR velero/crds && \
	$(YAMLFMT) $$TMPDIR/*.yaml && \
	if ! diff -rq $$TMPDIR $(VELERO_CRDS_DIR) > /dev/null 2>&1; then \
		echo "ERROR: CRDs are out of sync with chart version $(VELERO_VERSION). Run 'make extract-crds'."; \
		rm -rf $$TMPDIR; \
		exit 1; \
	fi && \
	rm -rf $$TMPDIR && \
	echo "CRDs are in sync with chart version $(VELERO_VERSION)."

# Update chart dependency and extract CRDs
update-chart:
	helm dependency update deploy/
	$(MAKE) extract-crds

.PHONY: extract-crds verify-crds update-chart
