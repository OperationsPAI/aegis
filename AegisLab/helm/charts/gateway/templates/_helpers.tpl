{{/*
gateway.fullname — canonical Service / Deployment name. MUST remain
`<release>-gateway` because the parent ConfigMap renders consumer-side
gateway routes that other services dial through `<release>-gateway:<port>`.
*/}}
{{- define "gateway.fullname" -}}
{{- printf "%s-gateway" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "gateway.image" -}}
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

{{- define "gateway.imagePullPolicy" -}}
{{- $global := .Values.global | default dict -}}
{{- $images := $global.images | default dict -}}
{{- $cfg := $images.rcabench | default dict -}}
{{- $cfg.pullPolicy | default "IfNotPresent" -}}
{{- end -}}
