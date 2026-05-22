{{/*
aegis-points.job — chart-bound PointManifest import for benchmark charts.

Usage (in a consumer chart's templates/aegis-points.yaml):

    {{ include "aegis-points.job" . }}

The include is OPT-IN: it emits nothing unless .Values.aegis.points.enabled
is true, so adding the dependency does not change behaviour for charts that
haven't authored manifests yet.

When enabled, it emits a Job + ConfigMap that POSTs every
aegis-points/*.yaml under the consumer chart to
/v1beta/systems/{sys}/points/import via 'aegisctl manifest import-dir'.

Hook weight ordering (pairs with #458 onboard Job):

  -10  aegis-onboard-job        (system identity + chart binding)
  -6   aegis-points ConfigMap   (this template)
  -5   aegis-points-import Job  (this template)
   0   consumer chart workloads

Required values when .Values.aegis.points.enabled is true:

  aegis:
    chaosServer: http://aegis-chaos.aegis.svc:8082
    points:
      enabled: true
      keepGoing: false           # default false — fail closed on any per-file error
    aegisctlImage: opspai/aegisctl:latest
    serviceAccount: ""           # optional; defaults to <Release.Name>-aegis
    tokenSecret: ""              # optional; key "token"
    imagePullPolicy: IfNotPresent

If aegis-points/*.yaml is absent under the consumer chart, the template
silently emits nothing — so a chart can ship the include before authoring
its first manifest.
*/}}

{{- define "aegis-points.job" -}}
{{- if and .Values.aegis .Values.aegis.points (eq (toString (default false .Values.aegis.points.enabled)) "true") -}}
{{- $files := .Files.Glob "aegis-points/*.yaml" -}}
{{- if $files -}}
{{- $keepGoing := default false .Values.aegis.points.keepGoing -}}
{{- $sa := .Values.aegis.serviceAccount | default (printf "%s-aegis" .Release.Name) -}}
{{- $image := .Values.aegis.aegisctlImage | default "opspai/aegisctl:latest" -}}
{{- $pullPolicy := .Values.aegis.imagePullPolicy | default "IfNotPresent" -}}
{{- $chaosServer := required "aegis.chaosServer is required when aegis.points.enabled is true" .Values.aegis.chaosServer -}}
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}-aegis-points
  labels:
    app.kubernetes.io/managed-by: {{ .Release.Service }}
    app.kubernetes.io/instance: {{ .Release.Name }}
    aegis.io/component: points-manifest
  annotations:
    helm.sh/hook: post-install,post-upgrade
    helm.sh/hook-weight: "-6"
    helm.sh/hook-delete-policy: before-hook-creation,hook-succeeded
data:
  {{- range $path, $bytes := $files }}
  {{ base $path }}: |
{{ $bytes | toString | indent 4 }}
  {{- end }}
---
apiVersion: batch/v1
kind: Job
metadata:
  name: {{ .Release.Name }}-aegis-points-import
  labels:
    app.kubernetes.io/managed-by: {{ .Release.Service }}
    app.kubernetes.io/instance: {{ .Release.Name }}
    aegis.io/component: points-import
  annotations:
    helm.sh/hook: post-install,post-upgrade
    helm.sh/hook-weight: "-5"
    helm.sh/hook-delete-policy: before-hook-creation,hook-succeeded
spec:
  backoffLimit: 0
  template:
    metadata:
      labels:
        aegis.io/component: points-import
    spec:
      restartPolicy: Never
      serviceAccountName: {{ $sa }}
      containers:
        - name: import-points
          image: {{ $image }}
          imagePullPolicy: {{ $pullPolicy }}
          env:
            - name: AEGIS_SERVER
              value: {{ $chaosServer | quote }}
            {{- with .Values.aegis.tokenSecret }}
            - name: AEGIS_TOKEN
              valueFrom:
                secretKeyRef:
                  name: {{ . }}
                  key: token
            {{- end }}
          command: ["aegisctl"]
          args:
            - manifest
            - import-dir
            - /etc/aegis-points
            {{- if $keepGoing }}
            - --keep-going
            {{- end }}
          volumeMounts:
            - name: points
              mountPath: /etc/aegis-points
              readOnly: true
      volumes:
        - name: points
          configMap:
            name: {{ .Release.Name }}-aegis-points
{{- end -}}
{{- end -}}
{{- end -}}
