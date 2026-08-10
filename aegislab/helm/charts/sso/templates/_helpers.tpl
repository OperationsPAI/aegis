{{/*
sso.* helpers.

Each helper resolves its values from one of two scopes:

  - Subchart scope (called from this subchart's own templates): `.Values`
    IS the subchart's merged values — `.Values.image`, `.Values.privateKey`,
    etc. exist at the root.

  - Parent scope (called from the parent rcabench chart, e.g.
    `include "sso.secretName" .` from helm/templates/deployment.yaml's
    aegis-api volume mount): `.Values.sso` is the merged subchart values.

The `sso._cfg` shim picks the right one by probing for a subchart-only
field (`config`). Keeps consumer-side `include` calls working unchanged
after extraction.
*/}}
{{- define "sso._cfg" -}}
{{- if hasKey .Values "config" -}}
{{- .Values | toYaml -}}
{{- else if hasKey .Values "sso" -}}
{{- (index .Values "sso") | toYaml -}}
{{- else -}}
{}
{{- end -}}
{{- end -}}

{{- define "sso.name" -}}
{{- $cfg := include "sso._cfg" . | fromYaml -}}
{{- default "sso" $cfg.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
sso.fullname — canonical Service / Deployment name for the aegis-sso
microservice. MUST remain `<release>-sso` because consumer charts
(aegis-api configmap upstream routes, gateway proxy, jwks URL, etc.)
hard-code this name. Do NOT change without auditing all consumers.
*/}}
{{- define "sso.fullname" -}}
{{- $cfg := include "sso._cfg" . | fromYaml -}}
{{- if $cfg.fullnameOverride -}}
{{- $cfg.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default "sso" $cfg.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
sso.labels — emits the standard Aegis label set. Note that
`helm.sh/chart` is hard-coded to the **parent** rcabench chart so the
rendered output stays bit-identical to the pre-extraction monolith
(consumers and `kubectl label` selectors may match on this). When the
sso subchart is ever lifted out of the rcabench monorepo and consumed
independently, switch this back to `.Chart.Name`/`.Chart.Version`.
*/}}
{{- define "sso.labels" -}}
app.kubernetes.io/name: {{ include "sso.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: rcabench-0.1.0
{{- end -}}

{{- define "sso.selectorLabels" -}}
app.kubernetes.io/name: {{ include "sso.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
sso.image — render the container image string. The SSO container reuses
the same `opspai/rcabench` family by default but can be pinned to a
different repository via `image.*`. Registry prefix comes from
`global.imageRegistry` (always read from the root scope — `global` is
shared between parent and subcharts by Helm).
*/}}
{{- define "sso.image" -}}
{{- $cfg := include "sso._cfg" . | fromYaml -}}
{{- $global := .Values.global | default dict -}}
{{- $registry := $global.imageRegistry | default "" -}}
{{- $image := $cfg.image | default dict -}}
{{- $name := $image.repository -}}
{{- $tag := $image.tag | default "latest" | toString -}}
{{- if $registry -}}
{{- printf "%s/%s:%s" $registry $name $tag -}}
{{- else -}}
{{- printf "%s:%s" $name $tag -}}
{{- end -}}
{{- end -}}

{{/*
sso.secretName — name of the Kubernetes Secret holding `sso-private.pem`.
Either user-supplied (`privateKey.existingSecret`) or auto-derived from
`sso.fullname` (`<release>-sso-key`). Called from BOTH the SSO subchart's
own templates AND parent-chart templates that mount the same secret into
aegis-api / runtime-worker (deployment.yaml volume `sso-private-key`).
*/}}
{{- define "sso.secretName" -}}
{{- $cfg := include "sso._cfg" . | fromYaml -}}
{{- $pk := $cfg.privateKey | default dict -}}
{{- if $pk.existingSecret -}}
{{- $pk.existingSecret -}}
{{- else -}}
{{- printf "%s-key" (include "sso.fullname" .) -}}
{{- end -}}
{{- end -}}
