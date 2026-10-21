{{- define "velero.resources" -}}
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

{{/* Velero's install CLI uses zero to omit a resource quantity. */}}
{{- define "velero.resourceFlag" -}}
{{- $quantity := toString . -}}
{{- if has $quantity (list "NONE" "unlimited") -}}
{{- $quantity = "0" -}}
{{- end -}}
{{- if not (regexMatch "^[+]?([0-9]+([.][0-9]*)?|[.][0-9]+)([numkMGTPE]|[KMGTPE]i|[eE][+-]?[0-9]+)?$" $quantity) -}}
{{- fail (printf "invalid Velero resource quantity: %q" $quantity) -}}
{{- end -}}
{{/* Validation excludes quotes and shell syntax, so single quoting is safe. */}}
{{- printf "'%s'" $quantity -}}
{{- end -}}
