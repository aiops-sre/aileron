{{/*
Expand the name of the chart.
*/}}
{{- define "oie.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "oie.fullname" -}}
{{- printf "%s" .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "oie.labels" -}}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
app.kubernetes.io/name: oie
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: alerthub-enterprise
app.kubernetes.io/version: {{ .Values.images.oie.tag | quote }}
{{- end }}

{{- define "oie.selectorLabels" -}}
app.kubernetes.io/name: oie
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "oie.imagePullSecrets" -}}
imagePullSecrets:
  - name: {{ .Values.global.imagePullSecret }}
{{- end }}
