ARO_HCP_IMAGE_ACR ?= {{ .acr.svc.name }}
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.{{ .acrDNSSuffix }}
MGMT_AGENT_IMAGE_REPOSITORY ?= {{ .mgmtAgent.image.repository }}

NAMESPACE ?= {{ .mgmtAgent.k8s.namespace }}
