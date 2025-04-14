{{/*
Return the name of the chart.
*/}}
{{- define "backplane-api.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "backplane-api.fullname" -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Return the chart name and version.
*/}}
{{- define "backplane-api.chart" -}}
{{ .Chart.Name }}-{{ .Chart.Version }}
{{- end -}}
