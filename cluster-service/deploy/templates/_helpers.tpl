{{- define "cluster-service.armHelperIdentityClientId" -}}
{{- if and .Values.azureClustersServiceArmHelperIdentityClientId .Values.azureClustersServiceArmHelperIdentityCertName -}}
{{- .Values.azureClustersServiceArmHelperIdentityClientId -}}
{{- else -}}
{{- .Values.azureArmHelperIdentityClientId -}}
{{- end -}}
{{- end -}}

{{- define "cluster-service.armHelperIdentityCertName" -}}
{{- if and .Values.azureClustersServiceArmHelperIdentityClientId .Values.azureClustersServiceArmHelperIdentityCertName -}}
{{- .Values.azureClustersServiceArmHelperIdentityCertName -}}
{{- else -}}
{{- .Values.azureArmHelperIdentityCertName -}}
{{- end -}}
{{- end -}}
