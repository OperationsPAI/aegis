{{/*
edge-proxy.fullname — canonical Service / Deployment name. MUST remain
`<release>-edge-proxy` because byte-cluster anchors a Volcengine LB EIP on
this Service; a name change would re-create the CLB and churn the EIP.
*/}}
{{- define "edge-proxy.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride -}}
{{- else -}}
{{- printf "%s-edge-proxy" .Release.Name -}}
{{- end -}}
{{- end -}}

{{/*
edge-proxy.image — Caddy image string. Replicates the parent `helm.image`
helper exactly (global registry support + @-digest handling). imageConfig is
sourced from `global.images.caddy` because `global` is the only value shared
between the umbrella and this subchart.
*/}}
{{- define "edge-proxy.image" -}}
{{- $global := .Values.global | default dict -}}
{{- $images := $global.images | default dict -}}
{{- $imageConfig := $images.caddy | default dict -}}
{{- $registry := $global.imageRegistry -}}
{{- $name := $imageConfig.repository | default $imageConfig.name -}}
{{- $tag := $imageConfig.tag | default "latest" | toString -}}
{{- $imageName := "" -}}
{{- if $registry -}}
  {{- $imageName = printf "%s/%s" $registry $name -}}
{{- else -}}
  {{- $imageName = $name -}}
{{- end -}}
{{- if contains "@" $tag -}}
  {{- printf "%s%s" $imageName $tag -}}
{{- else -}}
  {{- printf "%s:%s" $imageName $tag -}}
{{- end -}}
{{- end -}}

{{- define "edge-proxy.imagePullPolicy" -}}
{{- $global := .Values.global | default dict -}}
{{- $images := $global.images | default dict -}}
{{- $imageConfig := $images.caddy | default dict -}}
{{- $imageConfig.pullPolicy | default "IfNotPresent" -}}
{{- end -}}
