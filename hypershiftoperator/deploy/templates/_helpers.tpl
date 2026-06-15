{{- define "hypershiftoperator.image" -}}
{{- if .Values.hoImageOverride -}}
{{- .Values.hoImageOverride -}}
{{- else -}}
{{- .Values.image }}@{{ .Values.imageDigest -}}
{{- end -}}
{{- end -}}
