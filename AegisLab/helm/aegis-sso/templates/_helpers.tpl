{{- define "aegis-sso.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "aegis-sso.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "aegis-sso.labels" -}}
app.kubernetes.io/name: {{ include "aegis-sso.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "aegis-sso.selectorLabels" -}}
app.kubernetes.io/name: {{ include "aegis-sso.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "aegis-sso.image" -}}
{{- $registry := .Values.global.imageRegistry -}}
{{- $name := .Values.image.repository -}}
{{- $tag := .Values.image.tag | default "latest" | toString -}}
{{- if $registry -}}
{{- printf "%s/%s:%s" $registry $name $tag -}}
{{- else -}}
{{- printf "%s:%s" $name $tag -}}
{{- end -}}
{{- end -}}

{{- define "aegis-sso.secretName" -}}
{{- if .Values.privateKey.existingSecret -}}
{{- .Values.privateKey.existingSecret -}}
{{- else -}}
{{- printf "%s-key" (include "aegis-sso.fullname" .) -}}
{{- end -}}
{{- end -}}
