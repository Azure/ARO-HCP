ARO_HCP_IMAGE_ACR ?= {{ .acr.svc.name }}
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.{{ .acrDNSSuffix }}
SESSION_GATE_IMAGE_REPOSITORY ?= {{ .sessiongate.image.repository }}

NAMESPACE ?= {{ .sessiongate.k8s.namespace }}