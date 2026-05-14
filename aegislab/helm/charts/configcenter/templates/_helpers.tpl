{{/*
configcenter.fullname — canonical Service / Deployment name. MUST remain
`<release>-configcenter` because consumer charts may dial this DNS name
directly.
*/}}
{{- define "configcenter.fullname" -}}
{{- printf "%s-configcenter" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "configcenter.image" -}}
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

{{- define "configcenter.imagePullPolicy" -}}
{{- $global := .Values.global | default dict -}}
{{- $images := $global.images | default dict -}}
{{- $cfg := $images.rcabench | default dict -}}
{{- $cfg.pullPolicy | default "IfNotPresent" -}}
{{- end -}}
