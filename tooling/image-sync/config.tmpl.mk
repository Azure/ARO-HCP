ARO_HCP_IMAGE_ACR ?= {{ .svcAcrName }}
ARO_HCP_BASE_IMAGE ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
ARO_HCP_IMAGE_SYNC_IMAGE ?= $(ARO_HCP_BASE_IMAGE)/{{ .imageSyncImageRepo }}
