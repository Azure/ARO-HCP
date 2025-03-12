ARO_HCP_IMAGE_ACR ?= {{ .acr.svc.name }}
ARO_HCP_BASE_IMAGE ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
ARO_HCP_IMAGE_SYNC_IMAGE ?= $(ARO_HCP_BASE_IMAGE)/{{ .imageSync.componentSync.image.repository }}
