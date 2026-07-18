{{- define "oncall.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "oncall.fullname" -}}
{{- printf "%s-%s" .Release.Name (include "oncall.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "oncall.labels" -}}
app.kubernetes.io/name: {{ include "oncall.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "oncall.selectorLabels" -}}
app.kubernetes.io/name: {{ include "oncall.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "oncall.scheduleConfigMap" -}}
{{- if .Values.existingConfigMap -}}{{ .Values.existingConfigMap }}{{- else -}}{{ include "oncall.fullname" . }}-schedule{{- end -}}
{{- end -}}
