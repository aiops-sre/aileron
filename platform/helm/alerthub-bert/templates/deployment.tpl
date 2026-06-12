apiVersion: apps/v1
kind: Deployment
metadata:
  name: alerthub-bert-service
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "alerthub.labels" . | nindent 4 }}
    app.kubernetes.io/component: bert-embeddings
    app: alerthub-bert-service
spec:
  replicas: 1
  selector:
    matchLabels:
      app: alerthub-bert-service
  template:
    metadata:
      labels:
        app: alerthub-bert-service
        {{- include "alerthub.selectorLabels" . | nindent 8 }}
        app.kubernetes.io/component: bert-embeddings
    spec:
      {{- include "alerthub.imagePullSecrets" . | nindent 6 }}
      terminationGracePeriodSeconds: 30
      containers:
        - name: bert-service
          image: "{{ .Values.images.bert.repository }}:{{ .Values.imageTag | default .Values.images.bert.tag }}"
          imagePullPolicy: {{ .Values.images.bert.pullPolicy }}
          ports:
            - name: http
              containerPort: 8765
              protocol: TCP
          env:
            - name: PORT
              value: "8765"
            - name: HOST
              value: "0.0.0.0"
            - name: LOG_LEVEL
              value: {{ .Values.config.logLevel | quote }}
            - name: MODEL_NAME
              value: "bert-base-uncased"
            - name: BATCH_SIZE
              value: "32"
            - name: MAX_LENGTH
              value: "512"
            - name: ALERTHUB_BACKEND_URL
              value: "http://alerthub-backend.{{ .Release.Namespace }}.svc.cluster.local:3000"
            - name: REDIS_URL
              value: "redis://redis-cluster.{{ .Release.Namespace }}.svc.cluster.local:6379/7"
            {{- if .Values.bert.httpProxy }}
            - name: http_proxy
              value: {{ .Values.bert.httpProxy | quote }}
            - name: HTTP_PROXY
              value: {{ .Values.bert.httpProxy | quote }}
            {{- end }}
            {{- if .Values.bert.httpsProxy }}
            - name: https_proxy
              value: {{ .Values.bert.httpsProxy | quote }}
            - name: HTTPS_PROXY
              value: {{ .Values.bert.httpsProxy | quote }}
            {{- end }}
          resources:
            {{- toYaml .Values.bert.resources | nindent 12 }}
          livenessProbe:
            httpGet:
              path: /health
              port: 8765
            initialDelaySeconds: 60
            periodSeconds: 30
            timeoutSeconds: 15
            failureThreshold: 5
          readinessProbe:
            httpGet:
              path: /health
              port: 8765
            initialDelaySeconds: 30
            periodSeconds: 15
            timeoutSeconds: 15
            failureThreshold: 5
          startupProbe:
            httpGet:
              path: /health
              port: 8765
            initialDelaySeconds: 30
            periodSeconds: 10
            failureThreshold: 30
