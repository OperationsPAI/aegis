{{/*
redis.fullname — canonical Service/StatefulSet name. MUST remain
`<release>-redis`; the parent `[redis]` ConfigMap section and some
overlays' JuiceFS metaurl reference it by DNS.
*/}}
{{- define "redis.fullname" -}}
{{- printf "%s-redis" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "redis.image" -}}
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

{{- define "redis.imagePullPolicy" -}}
{{- .Values.image.pullPolicy | default "IfNotPresent" -}}
{{- end -}}
