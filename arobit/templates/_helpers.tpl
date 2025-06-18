{{/*
Expand the name of the chart.
*/}}
{{- define "arobit.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "arobit.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "arobit.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "arobit.labels" -}}
helm.sh/chart: {{ include "arobit.chart" . }}
{{- end }}

{{/*
Create the name of the forwarder service account to use
*/}}
{{- define "arobit.forwarder.serviceAccountName" -}}
{{- if .Values.forwarder.serviceAccount.create -}}
    {{ default (printf "%s-forwarder" (include "arobit.name" .)) .Values.forwarder.serviceAccount.name }}
{{- else -}}
    {{ default "default" .Values.forwarder.serviceAccount.name }}
{{- end -}}
{{- end -}}

{{/*
Create the name of the aggregator service account to use
*/}}
{{- define "arobit.aggregator.serviceAccountName" -}}
{{- if .Values.aggregator.serviceAccount.create -}}
    {{ default (printf "%s-aggregator" (include "arobit.name" .)) .Values.aggregator.serviceAccount.name }}
{{- else -}}
    {{ default "default" .Values.aggregator.serviceAccount.name }}
{{- end -}}
{{- end -}}

{{/*
Renders a value that contains template.
Usage:
{{ include "arobit.tplValue" (dict "value" .Values.path.to.the.Value "context" $) }}
*/}}
{{- define "arobit.tplValue" -}}
    {{- if typeIs "string" .value }}
        {{- tpl .value .context }}
    {{- else }}
        {{- tpl (.value | toYaml) .context }}
    {{- end }}
{{- end -}}
