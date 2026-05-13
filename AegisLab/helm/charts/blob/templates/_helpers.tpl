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

{{/*
blob.tomlSection — Render the [blob.client] / [blob.buckets.*] / [share]
TOML fragment for embedding in a unified config file. Both the rcabench
parent (which assembles a shared config.prod.toml for aegis-api,
runtime-worker, sso, and blob itself) and standalone consumers (e.g.,
AgentM) include this helper to keep blob's wire format in lockstep with
the schema declared in this subchart's values.yaml.

Usage from parent or external consumer:
  {{ include "blob.tomlSection" (dict "ctx" . "indent" 4) }}

`ctx` is the calling template's `.` (so the helper can read its
`Values`); `indent` is the number of leading spaces every emitted line
must carry (matches the indentation of the surrounding TOML block).
The helper produces fully-indented output and starts with a leading
newline — drop it straight after a non-blank line in your ConfigMap
without any further indent/nindent pipeline.

Dual-context lookup: when called from the parent chart, blob values
live at `.Values.blob.*`; when called from inside this subchart (or
from an external chart that omits the `blob:` wrapper), they live
directly at `.Values.*`. The `default` chain below makes both shapes
work without callers needing to adapt.
*/}}
{{- define "blob.tomlSection" -}}
{{- $ctx := .ctx | default . -}}
{{- $pad := repeat (int (.indent | default 4)) " " -}}
{{- $blob := $ctx.Values.blob | default $ctx.Values | default dict -}}
{{- with $blob.client | default dict }}

{{ $pad }}[blob.client]
{{ $pad }}mode = {{ .mode | default "local" | quote }}
{{- if .endpoint }}
{{ $pad }}endpoint = {{ .endpoint | quote }}
{{- end }}
{{- end }}
{{- range $name, $b := ($blob.buckets | default dict) }}

{{ $pad }}[blob.buckets.{{ $name }}]
{{ $pad }}driver = {{ $b.driver | quote }}
{{- if eq $b.driver "s3" }}
{{ $pad }}endpoint = {{ $b.endpoint | quote }}
{{ $pad }}bucket = {{ $b.bucket | quote }}
{{ $pad }}region = {{ $b.region | default "us-east-1" | quote }}
{{ $pad }}use_ssl = {{ $b.use_ssl | default false }}
{{ $pad }}path_style = {{ $b.path_style | default true }}
{{- if $b.access_key_env }}
{{ $pad }}access_key_env = {{ $b.access_key_env | quote }}
{{- end }}
{{- if $b.secret_key_env }}
{{ $pad }}secret_key_env = {{ $b.secret_key_env | quote }}
{{- end }}
{{- end }}
{{- if eq $b.driver "localfs" }}
{{ $pad }}root = {{ $b.root | quote }}
{{- end }}
{{- end }}
{{- with $blob.share | default dict }}

{{ $pad }}[share]
{{ $pad }}bucket               = {{ .bucket | default "shared" | quote }}
{{ $pad }}public_base_url      = {{ .publicBaseURL | default "" | quote }}
{{ $pad }}default_ttl_seconds  = {{ .defaultTtlSeconds | default 604800 | int64 }}
{{ $pad }}max_ttl_seconds      = {{ .maxTtlSeconds | default 2592000 | int64 }}
{{ $pad }}max_views            = {{ .maxViews | default 10000 | int64 }}
{{ $pad }}max_upload_bytes     = {{ .maxUploadBytes | default 1073741824 | int64 }}
{{ $pad }}user_quota_bytes     = {{ .userQuotaBytes | default 10737418240 | int64 }}
{{- end }}
{{- end -}}
