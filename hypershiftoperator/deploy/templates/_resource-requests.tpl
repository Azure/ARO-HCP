{{- define "hypershift.additionalMinimalResourceRequests" -}}
{{- $targets := .Files.Get "regular-resource-targets.yaml" | fromYaml -}}
{{- $seen := dict -}}
{{- range list "kube-apiserver" "openshift-controller-manager" "cluster-policy-controller" "kube-controller-manager" "openshift-apiserver" "etcd" "ovnkube-control-plane" -}}
{{- $_ := set $seen (printf "%s/%s" . .) true -}}
{{- end -}}
{{- range $id, $request := .Values.additionalMinimalResourceRequests -}}
{{- $deployment := required (printf "%s: deploymentName is required" $id) $request.deploymentName -}}
{{- $container := required (printf "%s: containerName is required" $id) $request.containerName -}}
{{- if not (has $container (index $targets $deployment | default list)) -}}
{{- fail (printf "%s: unsupported regular-container resource target %s/%s" $id $deployment $container) -}}
{{- end -}}
{{- $suffix := printf "%s.%s" $deployment $container -}}
{{- if gt (len $suffix) 63 -}}
{{- fail (printf "%s: invalid resource request override annotation key resource-request-override.hypershift.openshift.io/%s: name part must be no more than 63 characters" $id $suffix) -}}
{{- end -}}
{{- $key := printf "%s/%s" $deployment $container -}}
{{- if hasKey $seen $key -}}
{{- fail (printf "%s: duplicate resource target %s (including existing minimal entries)" $id $key) -}}
{{- end -}}
{{- $_ := set $seen $key true -}}
{{- if or $request.cpu $request.memory }}
- deploymentName: {{ $deployment | quote }}
  containerName: {{ $container | quote }}
  {{- with $request.cpu }}
  cpu: {{ . | quote }}
  {{- end }}
  {{- with $request.memory }}
  memory: {{ . | quote }}
  {{- end }}
{{- end -}}
{{- end -}}
{{- end -}}
