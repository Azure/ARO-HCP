{{- define "installNamespace" }}
{{- if .Values.global.namespace }}
{{- printf "%s" .Values.global.namespace }}
{{- else }}
{{- printf "multicluster-engine" }}
{{- end }}
{{- end }}

{{- define "commonCN" }}
{{- printf "clusterlifecycle-state-metrics-v2.%s.svc" .Values.global.namespace }}
{{- end }}

