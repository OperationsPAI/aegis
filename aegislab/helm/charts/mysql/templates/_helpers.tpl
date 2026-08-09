{{/*
mysql.fullname — canonical Service/StatefulSet name. MUST remain
`<release>-mysql` because the parent's `[database.mysql]` ConfigMap
section defaults `host` to this DNS name.
*/}}
{{- define "mysql.fullname" -}}
{{- printf "%s-mysql" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
mysql.image — render container image. Self-contained replica of the
parent's `helm.image` semantics so the subchart could be lifted out of
this monorepo unchanged.
*/}}
{{- define "mysql.image" -}}
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

{{- define "mysql.imagePullPolicy" -}}
{{- .Values.image.pullPolicy | default "IfNotPresent" -}}
{{- end -}}
