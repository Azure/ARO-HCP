ARO_HCP_IMAGE_ACR ?= {{ .acr.svc.name }}
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.{{ .acrDNSSuffix }}
CERT_EXPORTER_RBAC_CONTROLLER_IMAGE_REPOSITORY ?= {{ .certExporter.rbacController.image.repository }}

NAMESPACE ?= {{ .certExporter.k8s.namespace }}