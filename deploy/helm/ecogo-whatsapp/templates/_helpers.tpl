{{- define "ecogo.name" -}}{{ .Release.Name }}{{- end -}}

{{- define "ecogo.labels" -}}
app.kubernetes.io/part-of: ecogo-whatsapp
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end -}}

{{- define "ecogo.selector" -}}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{- define "ecogo.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end -}}

{{/* Environment shared by the Go services. withKeys adds the master key (api and worker only). */}}
{{- define "ecogo.env" -}}
- name: ECOGO_ENV
  value: {{ .root.Values.environment | quote }}
- name: ECOGO_LOG_LEVEL
  value: {{ .root.Values.config.logLevel | quote }}
- name: ECOGO_HTTP_ADDR
  value: ":8080"
- name: ECOGO_PUBLIC_APP_URL
  value: "https://{{ .root.Values.hosts.app }}"
- name: ECOGO_META_APP_ID
  value: {{ .root.Values.config.metaAppId | quote }}
- name: ECOGO_META_CONFIG_ID
  value: {{ .root.Values.config.metaConfigId | quote }}
- name: ECOGO_META_GRAPH_VERSION
  value: {{ .root.Values.config.metaGraphVersion | quote }}
- name: ECOGO_SMTP_HOST
  value: {{ .root.Values.config.smtpHost | quote }}
- name: ECOGO_SMTP_PORT
  value: {{ .root.Values.config.smtpPort | quote }}
- name: ECOGO_SMTP_USERNAME
  value: {{ .root.Values.config.smtpUsername | quote }}
- name: ECOGO_MAIL_FROM
  value: {{ .root.Values.config.mailFrom | quote }}
- name: ECOGO_DATABASE_URL
  valueFrom: { secretKeyRef: { name: {{ .root.Values.existingSecret }}, key: database-url } }
- name: ECOGO_META_APP_SECRET
  valueFrom: { secretKeyRef: { name: {{ .root.Values.existingSecret }}, key: meta-app-secret } }
- name: ECOGO_META_WEBHOOK_VERIFY_TOKEN
  valueFrom: { secretKeyRef: { name: {{ .root.Values.existingSecret }}, key: meta-webhook-verify-token } }
{{- if .withKeys }}
- name: ECOGO_MASTER_KEYS
  valueFrom: { secretKeyRef: { name: {{ .root.Values.existingSecret }}, key: master-keys } }
- name: ECOGO_APP_SECRET
  valueFrom: { secretKeyRef: { name: {{ .root.Values.existingSecret }}, key: app-secret } }
- name: ECOGO_SMTP_PASSWORD
  valueFrom: { secretKeyRef: { name: {{ .root.Values.existingSecret }}, key: smtp-password, optional: true } }
{{- end }}
{{- end -}}

{{/* One Deployment of the ecogo image. */}}
{{- define "ecogo.deployment" -}}
{{- $v := index .root.Values .component -}}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .root.Release.Name }}-{{ .component }}
  labels:
    {{- include "ecogo.labels" .root | nindent 4 }}
    app.kubernetes.io/component: {{ .component }}
spec:
  {{- if not (and (eq .component "api") .root.Values.api.autoscaling.enabled) }}
  replicas: {{ $v.replicas }}
  {{- end }}
  selector:
    matchLabels:
      {{- include "ecogo.selector" . | nindent 6 }}
  template:
    metadata:
      labels:
        {{- include "ecogo.labels" .root | nindent 8 }}
        app.kubernetes.io/component: {{ .component }}
    spec:
      {{- with .root.Values.imagePullSecrets }}
      imagePullSecrets: {{- toYaml . | nindent 8 }}
      {{- end }}
      automountServiceAccountToken: false
      securityContext: {{- toYaml .root.Values.podSecurityContext | nindent 8 }}
      topologySpreadConstraints:
        - maxSkew: 1
          topologyKey: kubernetes.io/hostname
          whenUnsatisfiable: ScheduleAnyway
          labelSelector:
            matchLabels:
              {{- include "ecogo.selector" . | nindent 14 }}
      containers:
        - name: {{ .component }}
          image: {{ include "ecogo.image" .root }}
          imagePullPolicy: {{ .root.Values.image.pullPolicy }}
          args: [{{ .component | quote }}]
          env:
            {{- include "ecogo.env" (dict "root" .root "withKeys" (ne .component "ingest")) | nindent 12 }}
          ports:
            - name: http
              containerPort: 8080
          readinessProbe:
            httpGet: { path: /readyz, port: http }
            periodSeconds: 5
          livenessProbe:
            httpGet: { path: /healthz, port: http }
            periodSeconds: 10
          resources: {{- toYaml $v.resources | nindent 12 }}
          securityContext: {{- toYaml .root.Values.securityContext | nindent 12 }}
{{- end -}}
