ARO_HCP_SVC_ACR ?= {{ .svcAcrName }}
ARO_HCP_OCP_ACR ?= {{ .ocpAcrName }}
HO_IMAGE_TAG ?= {{ .hypershiftOperatorImageTag }}
HO_IMAGE_BASE ?= ${ARO_HCP_SVC_ACR}.azurecr.io/acm-d/rhtap-hypershift-operator
HO_IMAGE ?= ${HO_IMAGE_BASE}:${HO_IMAGE_TAG}
ED_IMAGE ?= ${ARO_HCP_SVC_ACR}.azurecr.io/external-dns/external-dns:${ED_IMAGE_TAG}
ED_IMAGE_TAG ?= {{ .externalDNSImageTag }}
ED_IMAGE_BASE ?= ${ARO_HCP_SVC_ACR}.azurecr.io/external-dns/external-dns
ED_IMAGE ?= ${ED_IMAGE_BASE}:${ED_IMAGE_TAG}

RESOURCEGROUP ?= {{ .managementClusterRG }}
REGIONAL_RESOURCEGROUP ?= {{ .regionRG }}
ZONE_NAME ?= {{ .regionalDNSSubdomain }}.{{ .baseDnsZoneName }}
AKS_NAME ?= {{ .aksName }}
HYPERSHIFT_NAMESPACE ?= {{ .hypershiftNamespace}}
EXTERNAL_DNS_MI_NAME ?= {{ .externalDNSManagedIdentityName }}

HO_CHART_DIR ?= deploy/helm/charts/hypershift-operator
