{{- define "ray-cluster.fullname" -}}
{{- printf "%s-ray" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "ray-cluster.namespace" -}}
{{- $ns := dig "k8s" "namespace" "" (.Values.global | default dict) -}}
{{- if $ns -}}
{{- $ns -}}
{{- else -}}
{{- .Release.Namespace -}}
{{- end -}}
{{- end -}}
