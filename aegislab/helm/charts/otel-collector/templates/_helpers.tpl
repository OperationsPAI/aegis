{{/*
otel-collector.fullname — the canonical Service / Deployment name for
the in-namespace OpenTelemetry Collector. MUST remain
`<release>-otel-collector` because consumer charts (SSO
`tracing.endpoint`, edge-proxy OTEL env vars, parent `[observability]`
ConfigMap section) hard-code this DNS name. Do NOT change without
auditing all consumers.
*/}}
{{- define "otel-collector.fullname" -}}
{{- printf "%s-otel-collector" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
otel-collector.image — render container image string. Re-implements the
small image-resolution logic so the subchart stays self-contained.
Inputs: .Values.image.{name|repository, tag, pullPolicy} and
.Values.global.imageRegistry (optional registry prefix).
*/}}
{{- define "otel-collector.image" -}}
{{- $global := .Values.global | default dict -}}
{{- $registry := $global.imageRegistry | default "" -}}
{{- $cfg := .Values.image | default dict -}}
{{- $name := $cfg.repository | default ($cfg.name | default "") -}}
{{- $tag := $cfg.tag | default "latest" | toString -}}
{{- $image := $name -}}
{{- if $registry -}}
{{- $image = printf "%s/%s" $registry $name -}}
{{- end -}}
{{- if contains "@" $tag -}}
{{- printf "%s%s" $image $tag -}}
{{- else -}}
{{- printf "%s:%s" $image $tag -}}
{{- end -}}
{{- end -}}

{{- define "otel-collector.imagePullPolicy" -}}
{{- .Values.image.pullPolicy | default "IfNotPresent" -}}
{{- end -}}
