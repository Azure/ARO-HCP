{{- define "cihealth.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "cihealth.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "cihealth.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "cihealth.labels" -}}
helm.sh/chart: {{ include "cihealth.chart" . }}
app.kubernetes.io/name: {{ include "cihealth.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
{{- end -}}

{{- define "cihealth.selectorLabels" -}}
app.kubernetes.io/name: {{ include "cihealth.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "cihealth.image" -}}
{{- if .Values.image.registry -}}
{{- printf "%s/%s:%s" .Values.image.registry .Values.image.repository .Values.image.tag -}}
{{- else -}}
{{- printf "%s:%s" .Values.image.repository .Values.image.tag -}}
{{- end -}}
{{- end -}}

{{- define "cihealth.postgresImage" -}}
{{- printf "%s:%s" .Values.postgres.image.repository .Values.postgres.image.tag -}}
{{- end -}}

{{- define "cihealth.appName" -}}
{{- printf "%s-app" (include "cihealth.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "cihealth.controllersName" -}}
{{- printf "%s-controllers" (include "cihealth.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "cihealth.postgresName" -}}
{{- printf "%s-postgres" (include "cihealth.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "cihealth.postgresPvcName" -}}
{{- printf "%s-postgres-data" (include "cihealth.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "cihealth.serviceAccountName" -}}
{{- include "cihealth.fullname" . | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "cihealth.secretProviderClassName" -}}
{{- printf "%s-postgres-secretprovider" (include "cihealth.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "cihealth.secretsStoreMountPath" -}}
/mnt/secrets-store
{{- end -}}

{{- define "cihealth.postgresUsernameFile" -}}
{{- printf "%s/postgres-username" (include "cihealth.secretsStoreMountPath" .) -}}
{{- end -}}

{{- define "cihealth.postgresPasswordFile" -}}
{{- printf "%s/postgres-password" (include "cihealth.secretsStoreMountPath" .) -}}
{{- end -}}

{{- define "cihealth.postgresDatabaseFile" -}}
{{- printf "%s/postgres-database" (include "cihealth.secretsStoreMountPath" .) -}}
{{- end -}}
