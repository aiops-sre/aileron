{{/*
============================================================
AlertHub Helm Helper Templates
============================================================
*/}}

{{/*
Expand the name of the chart.
*/}}
{{- define "alerthub.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "alerthub.fullname" -}}
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

{{/*
Create chart label value  (chart-name-version).
*/}}
{{- define "alerthub.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels applied to every resource.
*/}}
{{- define "alerthub.labels" -}}
helm.sh/chart: {{ include "alerthub.chart" . }}
app.kubernetes.io/name: {{ include "alerthub.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/part-of: alerthub-enterprise
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
{{- end }}

{{/*
Selector labels (used in matchLabels / Service selectors).
*/}}
{{- define "alerthub.selectorLabels" -}}
app.kubernetes.io/name: {{ include "alerthub.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Service account name.
*/}}
{{- define "alerthub.serviceAccountName" -}}
{{- if .Values.serviceAccount }}
{{- if .Values.serviceAccount.name }}
{{- .Values.serviceAccount.name }}
{{- else -}}
default
{{- end }}
{{- else -}}
default
{{- end }}
{{- end }}

{{/*
Fully-qualified DNS name for the backend service.
Usage: {{ include "alerthub.backendSvcFQDN" . }}
*/}}
{{- define "alerthub.backendSvcFQDN" -}}
alerthub-backend.{{ .Release.Namespace }}.svc.cluster.local
{{- end }}

{{/*
Image pull secret list — renders imagePullSecrets from global.imagePullSecret.
*/}}
{{- define "alerthub.imagePullSecrets" -}}
imagePullSecrets:
  - name: {{ .Values.global.imagePullSecret }}
{{- end }}
