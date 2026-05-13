{{/*
blob.fullname — the canonical Service / Deployment name for the
aegis-blob microservice. MUST remain `<release>-blob` because consumer
charts (gateway routes, aegis-api configmap blob endpoints) reference
this name directly.
*/}}
{{- define "blob.fullname" -}}
{{- printf "%s-blob" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
blob.image — render the container image string using the parent chart's
shared `global.images.rcabench` config so a single image-tag bump in the
parent flows to every microservice subchart.
*/}}
{{- define "blob.image" -}}
{{- $global := .Values.global | default dict -}}
{{- $registry := $global.imageRegistry | default "" -}}
{{- $images := $global.images | default dict -}}
{{- $cfg := $images.rcabench | default dict -}}
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

{{- define "blob.imagePullPolicy" -}}
{{- $global := .Values.global | default dict -}}
{{- $images := $global.images | default dict -}}
{{- $cfg := $images.rcabench | default dict -}}
{{- $cfg.pullPolicy | default "IfNotPresent" -}}
{{- end -}}
