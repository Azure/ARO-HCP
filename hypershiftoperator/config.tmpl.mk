ARO_HCP_IMAGE_ACR ?= {{ .svcAcrName }}
HO_IMAGE_TAG ?= {{ .hypershiftOperatorImageTag }}
ED_IMAGE_TAG ?= {{ .externalDNSImageTag }}
RESOURCEGROUP ?= {{ .managementClusterRG }}
REGIONAL_RESOURCEGROUP ?= {{ .regionRG }}
ZONE_NAME ?= {{ .regionalDNSSubdomain }}.{{ .baseDnsZoneName }}
