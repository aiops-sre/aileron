apiVersion: apps/v1
kind: Deployment
metadata:
  name: alerthub-frontend
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "alerthub.labels" . | nindent 4 }}
    app.kubernetes.io/component: frontend
    app: alerthub-frontend
spec:
  replicas: {{ .Values.frontend.replicas }}
  selector:
    matchLabels:
      app: alerthub-frontend
  template:
    metadata:
      labels:
        app: alerthub-frontend
        {{- include "alerthub.selectorLabels" . | nindent 8 }}
        app.kubernetes.io/component: frontend
    spec:
      {{- include "alerthub.imagePullSecrets" . | nindent 6 }}
      containers:
        - name: alerthub-frontend
          image: "{{ .Values.images.frontend.repository }}:{{ .Values.imageTag | default .Values.images.frontend.tag }}"
          imagePullPolicy: {{ .Values.images.frontend.pullPolicy }}
          ports:
            - name: http
              containerPort: 80
              protocol: TCP
          volumeMounts:
            - name: nginx-config
              mountPath: /etc/nginx/conf.d
              readOnly: true
          env:
            - name: BACKEND_URL
              value: "http://alerthub-backend:{{ .Values.backend.port }}"
          resources:
            {{- toYaml .Values.frontend.resources | nindent 12 }}
          livenessProbe:
            httpGet:
              path: /health
              port: 80
            initialDelaySeconds: 30
            periodSeconds: 10
            timeoutSeconds: 5
            failureThreshold: 3
          readinessProbe:
            httpGet:
              path: /health
              port: 80
            initialDelaySeconds: 10
            periodSeconds: 5
            timeoutSeconds: 5
            failureThreshold: 3
      volumes:
        - name: nginx-config
          configMap:
            name: nginx-config
