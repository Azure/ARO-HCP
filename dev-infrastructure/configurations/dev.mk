REGION ?= westus3
RESOURCEGROUP ?= aro-hcp-$(USER)-$(REGION)-$(AKSCONFIG)
REGIONAL_RESOURCEGROUP ?= aro-hcp-$(USER)-$(REGION)
GLOBAL_RESOURCEGROUP ?= global
ARO_HCP_IMAGE_ACR ?= arohcpdev
REGIONAL_ACR_NAME ?= arohcpdev$(shell echo $(CURRENTUSER) | sha256sum  | head -c 24)
BASE_DOMAIN ?= hcp.osadev.cloud
