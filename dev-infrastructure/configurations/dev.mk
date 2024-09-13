REGION ?= westus3
RESOURCEGROUP ?= aro-hcp-$(USER)-$(REGION)-$(AKSCONFIG)
REGIONAL_RESOURCEGROUP ?= aro-hcp-$(USER)-$(REGION)
GLOBAL_RESOURCEGROUP ?= global
ARO_HCP_IMAGE_ACR ?= arohcpdev
REGIONAL_ACR_NAME ?= arohcpdev$(shell echo $(CURRENTUSER) | sha256sum  | head -c 24)
REPOSITORIES_TO_SYNC ?= '{registry.k8s.io/external-dns/external-dns,quay.io/acm-d/rhtap-hypershift-operator,quay.io/pstefans/controlplaneoperator,quay.io/app-sre/uhc-clusters-service,quay.io/app-sre/ocm-clusters-service-sandbox}'
