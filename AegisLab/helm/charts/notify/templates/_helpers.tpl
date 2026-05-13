{{/*
notify.fullname — the canonical Service / Deployment name for the
aegis-notify microservice. MUST remain `<release>-notify` because
consumer charts (aegis-api configmap `notify_url`, edge-proxy / gateway
routes, etc.) hard-code this name. Do NOT change without auditing all
consumers.
*/}}
{{- define "notify.fullname" -}}
{{- printf "%s-notify" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
notify.image — render the container image string using the parent
chart's `helm.image` semantics. We deliberately re-implement the small
logic here (rather than depending on the parent's helper) so this
subchart stays self-contained and could be lifted out of the monorepo
unchanged.

Inputs (all under .Values.global, populated by the parent chart):
  global.imageRegistry      optional registry prefix
  global.images.rcabench    {name|repository, tag, pullPolicy}
*/}}
{{- define "notify.image" -}}
{{- $registry := .Values.global.imageRegistry | default "" -}}
{{- $cfg := .Values.global.images.rcabench -}}
{{- $name := $cfg.repository | default $cfg.name -}}
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

{{/*
notify.imagePullPolicy — pull policy from the shared rcabench image.
*/}}
{{- define "notify.imagePullPolicy" -}}
{{- .Values.global.images.rcabench.pullPolicy -}}
{{- end -}}
