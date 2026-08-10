{{- define "ray-cluster.fullname" -}}
{{- printf "%s-ray" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "ray-cluster.s3-env" -}}
{{- $rustfsEndpoint := printf "http://%s-rustfs.%s.svc.cluster.local:9000" .Release.Name (include "ray-cluster.namespace" .) -}}
- name: AWS_ACCESS_KEY_ID
  valueFrom:
    secretKeyRef:
      name: {{ .Values.s3.credentialsSecret }}
      key: {{ .Values.s3.accessKeyField }}
- name: AWS_SECRET_ACCESS_KEY
  valueFrom:
    secretKeyRef:
      name: {{ .Values.s3.credentialsSecret }}
      key: {{ .Values.s3.secretKeyField }}
- name: AWS_ENDPOINT_URL
  value: {{ $rustfsEndpoint }}
{{- end -}}

{{- define "ray-cluster.namespace" -}}
{{- $ns := dig "k8s" "namespace" "" (.Values.global | default dict) -}}
{{- if $ns -}}
{{- $ns -}}
{{- else -}}
{{- .Release.Namespace -}}
{{- end -}}
{{- end -}}
