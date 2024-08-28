REGION ?= westus3
RESOURCEGROUP ?= cs-integ-$(USER)-$(REGION)-$(AKSCONFIG)
REGIONAL_RESOURCEGROUP ?= cs-integ-$(USER)-$(REGION)
ARO_HCP_IMAGE_ACR ?= arohcpdev
REGIONAL_ACR_NAME ?= arohcpdev$(shell echo $(CURRENTUSER) | sha256sum  | head -c 24)

