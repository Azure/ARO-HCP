ARO_HCP_IMAGE_ACR ?= {{ .svcAcrName }}
ARO_HCP_IMAGE_ACR_URL ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
OC_MIRROR_IMAGE ?= $(ARO_HCP_IMAGE_ACR_URL)/{{ .imageSync.ocMirror.image.repository }}
OC_MIRROR_IMAGE_TAGGED ?= $(OC_MIRROR_IMAGE):$(COMMIT)

ARO_HCP_OCP_IMAGE_ACR ?= {{ .ocpAcrName }}
ARO_HCP_OCP_IMAGE_ACR_URL ?= ${ARO_HCP_OCP_IMAGE_ACR}.azurecr.io
