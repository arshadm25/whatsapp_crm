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

{{/* ecogo.id quotes a numeric ID as written: values files and --reuse-values turn long numbers
into floats, which quote would render as 8.8789121708352e+14. */}}
{{- define "ecogo.id" -}}
{{- if kindIs "float64" . -}}{{ printf "%.0f" . | quote }}{{- else -}}{{ . | quote }}{{- end -}}
{{- end -}}

{{/* Environment shared by the Go services. withKeys adds the master key (api and worker only). */}}
{{- define "ecogo.env" -}}
- name: ECOGO_ENV
  value: {{ .root.Values.environment | quote }}
- name: ECOGO_LOG_LEVEL
  value: {{ .root.Values.config.logLevel | quote }}
- name: ECOGO_VERSION
  value: {{ .root.Values.image.tag | default .root.Chart.AppVersion | quote }}
- name: ECOGO_HTTP_ADDR
  value: ":8080"
- name: ECOGO_PUBLIC_APP_URL
  value: "https://{{ .root.Values.hosts.app }}"
- name: ECOGO_META_APP_ID
  value: {{ include "ecogo.id" .root.Values.config.metaAppId }}
- name: ECOGO_META_CONFIG_ID
  value: {{ include "ecogo.id" .root.Values.config.metaConfigId }}
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
- name: ECOGO_S3_ENDPOINT
  value: {{ .root.Values.config.s3Endpoint | quote }}
- name: ECOGO_S3_BUCKET
  value: {{ .root.Values.config.s3Bucket | default (printf "%s-media" .root.Release.Name) | quote }}
- name: ECOGO_S3_USE_SSL
  value: {{ .root.Values.config.s3UseSSL | quote }}
- name: ECOGO_S3_ACCESS_KEY
  valueFrom: { secretKeyRef: { name: {{ .root.Values.existingSecret }}, key: s3-access-key } }
- name: ECOGO_S3_SECRET_KEY
  valueFrom: { secretKeyRef: { name: {{ .root.Values.existingSecret }}, key: s3-secret-key } }
- name: ECOGO_SELLER_NAME
  value: {{ .root.Values.config.sellerName | quote }}
- name: ECOGO_SELLER_GSTIN
  value: {{ .root.Values.config.sellerGstin | quote }}
- name: ECOGO_SELLER_ADDRESS
  value: {{ .root.Values.config.sellerAddress | quote }}
- name: ECOGO_SELLER_SAC
  value: {{ .root.Values.config.sellerSac | quote }}
- name: ECOGO_GST_RATE_BP
  value: {{ .root.Values.config.gstRateBp | quote }}
- name: ECOGO_META_MARKUP_BP
  value: {{ .root.Values.config.metaMarkupBp | quote }}
- name: ECOGO_RAZORPAY_KEY_ID
  valueFrom: { secretKeyRef: { name: {{ .root.Values.existingSecret }}, key: razorpay-key-id, optional: true } }
- name: ECOGO_RAZORPAY_KEY_SECRET
  valueFrom: { secretKeyRef: { name: {{ .root.Values.existingSecret }}, key: razorpay-key-secret, optional: true } }
- name: ECOGO_RAZORPAY_WEBHOOK_SECRET
  valueFrom: { secretKeyRef: { name: {{ .root.Values.existingSecret }}, key: razorpay-webhook-secret, optional: true } }
- name: ECOGO_META_CREDIT_LINE_ENABLED
  value: {{ .root.Values.config.creditLineEnabled | quote }}
- name: ECOGO_META_CREDIT_LINE_ID
  value: {{ .root.Values.config.creditLineId | quote }}
- name: ECOGO_META_PARTNER_TOKEN
  valueFrom: { secretKeyRef: { name: {{ .root.Values.existingSecret }}, key: meta-partner-token, optional: true } }
- name: ECOGO_AI_PROVIDER
  value: {{ .root.Values.config.aiProvider | quote }}
- name: ECOGO_AI_MODEL
  value: {{ .root.Values.config.aiModel | quote }}
- name: ECOGO_AI_API_KEY
  valueFrom: { secretKeyRef: { name: {{ .root.Values.existingSecret }}, key: ai-api-key, optional: true } }
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
