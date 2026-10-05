{{- define "admin.certificateRefresherResources" -}}
{{- $resources := dict -}}
{{- range $kind, $values := . -}}
{{- $quantities := dict -}}
{{- range $name, $value := $values -}}
{{- if not (has (toString $value) (list "NONE" "unlimited")) -}}
{{- $_ := set $quantities $name $value -}}
{{- end -}}
{{- end -}}
{{- if $quantities -}}
{{- $_ := set $resources $kind $quantities -}}
{{- end -}}
{{- end -}}
{{- with $resources -}}
resources:
  {{- toYaml . | nindent 2 }}
{{- end -}}
{{- end -}}
