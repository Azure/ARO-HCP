ARO_HCP_IMAGE_ACR ?= {{ .acr.svc.name }}
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.{{ .acrDNSSuffix }}
SWIFT_RECORDER_IMAGE_REPOSITORY ?= {{ .swiftRecorder.image.repository }}

NAMESPACE ?= {{ .swiftRecorder.k8s.namespace }}
