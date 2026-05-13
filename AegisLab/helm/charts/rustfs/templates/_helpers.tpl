{{/*
rustfs.fullname — canonical Service / Deployment name. MUST remain
`<release>-rustfs` because the blob microservice (and parent-chart
configmap blob bucket endpoints) reference this DNS name directly.
*/}}
{{- define "rustfs.fullname" -}}
{{- printf "%s-rustfs" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
rustfs.secretName — the Secret holding rustfs root credentials and the
per-bucket env aliases. Defaults to `rustfs-admin`; override via
`rustfs.auth.existingSecret` to point at a pre-created Secret. This
helper is referenced from parent templates too (Helm template helpers
are template-namespace-global within a release).
*/}}
{{- define "rustfs.secretName" -}}
{{- default "rustfs-admin" .Values.auth.existingSecret -}}
{{- end -}}

{{/*
rustfs.namespace — namespace for the rustfs resources. Reads
`global.k8s.namespace` first (mirrored from parent `configmap.k8s.namespace`),
falls back to `.Release.Namespace` so standalone `helm lint` works.
*/}}
{{- define "rustfs.namespace" -}}
{{- $ns := dig "k8s" "namespace" "" (.Values.global | default dict) -}}
{{- if $ns -}}
{{- $ns -}}
{{- else -}}
{{- .Release.Namespace -}}
{{- end -}}
{{- end -}}
