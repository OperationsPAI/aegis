{{/*
etcd.statefulsetName — canonical StatefulSet name: `<release>-etcd`.
Used as the `app` selector for both Services.
*/}}
{{- define "etcd.statefulsetName" -}}
{{- printf "%s-etcd" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
etcd.headlessName — headless Service `<release>-etcd-headless`. Used
as `serviceName` for the StatefulSet and embedded into the etcd
ETCD_INITIAL_ADVERTISE_PEER_URLS / ETCD_INITIAL_CLUSTER env vars.
*/}}
{{- define "etcd.headlessName" -}}
{{- printf "%s-etcd-headless" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
etcd.externalName — external (client-facing) Service
`<release>-etcd-external`. Used in ETCD_ADVERTISE_CLIENT_URLS.
*/}}
{{- define "etcd.externalName" -}}
{{- printf "%s-etcd-external" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "etcd.image" -}}
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

{{- define "etcd.imagePullPolicy" -}}
{{- .Values.image.pullPolicy | default "IfNotPresent" -}}
{{- end -}}
