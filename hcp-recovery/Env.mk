ARO_HCP_IMAGE_ACR ?= {{ .acr.svc.name }}
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.{{ .acrDNSSuffix }}
HCP_RECOVERY_IMAGE_REPOSITORY ?= {{ .hcpRecovery.image.repository }}
NAMESPACE ?= hcp-recovery
