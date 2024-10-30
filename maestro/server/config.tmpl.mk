EVENTGRID_NAME ?= {{ .maestroEventgridName}}
REGION_RG ?= {{ .regionRG }}
AKS_NAME ?= {{ .aksName }}
SVC_RG ?= {{ .serviceClusterRG }}
IMAGE_BASE ?= {{ .maestroImageBase }}
IMAGE_TAG ?= {{ .maestroImageTag }}
USE_CONTAINERIZED_DB ?= {{ not .maestroPostgresDeploy }}
USE_DATABASE_SSL ?= {{ ternary "enable" "disable" .maestroPostgresDeploy }}
ISTIO_RESTRICT_INGRESS ?= {{ .maestroRestrictIstioIngress }}
KEYVAULT_NAME ?= {{ .maestroKeyVaultName }}

MAESTRO_NAMESPACE_NAME ?= {{ .maestroServerNamespace }}
MAESTRO_SA_NAME = {{ .maestroServerServiceAccountName }}
MAESTRO_MI_NAME ?= {{ .maestroServerManagedIdentityName }}

CS_NAMESPACE_NAME ?= {{ .clusterServiceNamespace }}
CS_SA_NAME = {{ .clusterServiceServiceAccountName }}
