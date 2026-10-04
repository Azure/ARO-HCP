{{/* Omission sentinels apply per quantity. Validate before embedding values in hook JSON. */}}
{{- define "acm.resources" -}}
{{- $resources := dict -}}
{{- range $kind := list "requests" "limits" -}}
  {{- $quantities := dict -}}
  {{- range $name, $value := index $ $kind -}}
    {{- $quantity := toString $value -}}
    {{- if and (ne $quantity "NONE") (ne $quantity "unlimited") -}}
      {{- if not (regexMatch "^[+]?([0-9]+([.][0-9]*)?|[.][0-9]+)([numkMGTPE]|[KMGTPE]i|[eE][+-]?[0-9]+)?$" $quantity) -}}
        {{- fail (printf "invalid resource quantity for %s.%s: %q" $kind $name $quantity) -}}
      {{- end -}}
      {{- $_ := set $quantities $name $quantity -}}
    {{- end -}}
  {{- end -}}
  {{- if $quantities -}}
    {{- $_ := set $resources $kind $quantities -}}
  {{- end -}}
{{- end -}}
{{- toYaml $resources -}}
{{- end -}}
