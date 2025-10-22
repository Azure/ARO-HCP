# OADP Operator Helm Chart Generation

# Path to the upstream OADP operator bundle
# Adjust this if the oadp-operator repository is in a different location
OADP_BUNDLE_PATH ?= ../../oadp-operator/bundle/manifests


generate-chart:
	@echo "Regenerating OADP operator Helm chart..."
	@go run ../tooling/olm-bundle-repkg \
		-c olm-bundle-repkg-config.yaml \
		-b file://$(OADP_BUNDLE_PATH) \
		-s oadp-operator-scaffold \
		-o . \
		-l https://github.com/openshift/oadp-operator/tree/master/bundle
	@$(MAKE) -C ../ yamlfmt
	@echo "Chart regenerated successfully in oadp-operator/"
	@echo ""
	@echo "Note: The following CRD files are NOT regenerated and must be maintained manually:"
	@echo "  - oadp-operator/crds/0000_03_config-operator_01_securitycontextconstraints.crd.yaml"
	@echo "  - oadp-operator/crds/0000_10_config-operator_01_infrastructures-Default.crd.yaml"
	@echo "  - oadp-operator/crds/routes.crd.yaml"
	@echo ""
	@echo "The following files ARE now regenerated from oadp-operator-scaffold/:"
	@echo "  - oadp-operator/templates/acrpullbinding.yaml"
	@echo "  - oadp-operator/templates/azure-backup-storage.secret.yaml"
	@echo "  - oadp-operator/templates/cluster.infrastructure.yaml"
	@echo "  - oadp-operator/values.yaml"
.PHONY: regenerate-chart
