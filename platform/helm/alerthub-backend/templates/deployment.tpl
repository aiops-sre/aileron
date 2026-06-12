apiVersion: apps/v1
kind: Deployment
metadata:
  name: alerthub-backend
  namespace: {{ .Release.Namespace }}
  annotations:
    argocd.argoproj.io/sync-options: Replace=true
  labels:
    {{- include "alerthub.labels" . | nindent 4 }}
    app.kubernetes.io/component: backend
    app: alerthub-backend
spec:
  replicas: {{ .Values.backend.replicas }}
  selector:
    matchLabels:
      app: alerthub-backend
  template:
    metadata:
      labels:
        app: alerthub-backend
        {{- include "alerthub.selectorLabels" . | nindent 8 }}
        app.kubernetes.io/component: backend
    spec:
      {{- include "alerthub.imagePullSecrets" . | nindent 6 }}
      serviceAccountName: {{ include "alerthub.serviceAccountName" . }}
      {{- if .Values.podAntiAffinity.enabled }}
      affinity:
        podAntiAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
            - weight: 100
              podAffinityTerm:
                labelSelector:
                  matchLabels:
                    {{- include "alerthub.selectorLabels" . | nindent 20 }}
                topologyKey: kubernetes.io/hostname
      {{- end }}
      securityContext:
        runAsNonRoot: true
        runAsUser: 1000
        fsGroup: 1000
        seccompProfile:
          type: RuntimeDefault
      initContainers:
        {{- if index .Values "whisper" "enabled" | default false }}
        - name: init-whisperctl
          image: ghcr.io/aileron-platform/crypto-services/whisperctl:1.1.3
          imagePullPolicy: IfNotPresent
          command: ["/bin/sh", "-c"]
          args:
            - |
              set -e
              fetch() {
                ./whisperctl secret fetch \
                  --server whisper.example.com \
                  --client-certificate /etc/ssl/whisper/tls.crt \
                  --client-key /etc/ssl/whisper/tls.key \
                  --client-certificate-format PEM \
                  --namespace aileron-admins \
                  --secret-name "$1" \
                  --output-dir /tmp/secrets
              }
              fetch alerthub-app-secrets
              fetch alerthub-dsldap
              fetch alerthub-hcl
              fetch alerthub-infra
              fetch alerthub-jfrog
              fetch alerthub-kubeconfigs
          volumeMounts:
            - name: whisper-secrets
              mountPath: /tmp/secrets
            - name: whisper-cert
              mountPath: /etc/ssl/whisper
              readOnly: true
        {{- else }}
        - name: init-k8s-secrets
          image: "{{ .Values.images.redis.repository }}:{{ .Values.images.redis.tag }}"
          imagePullPolicy: IfNotPresent
          command: ["/bin/sh", "-c"]
          args:
            - |
              printf '{"DB_PASSWORD":"%s","DB_USER":"%s","JWT_SECRET":"%s","INTERNAL_SERVICE_TOKEN":"%s","REDIS_PASSWORD":"%s","AI_API_KEY":"%s","JWT_REFRESH_SECRET":"%s","OAUTH_CLIENT_SECRET":"%s","WEAVIATE_API_KEY":"%s","NETAPP_PASSWORD":"%s","LDAP_BIND_PASSWORD":"%s"}' \
                "$DB_PASSWORD" "$DB_USER" "$JWT_SECRET" "$INTERNAL_SERVICE_TOKEN" "$REDIS_PASSWORD" "$AI_API_KEY" "$JWT_REFRESH_SECRET" "$OAUTH_CLIENT_SECRET" "$WEAVIATE_API_KEY" "$NETAPP_PASSWORD" "$LDAP_BIND_PASSWORD" \
                > /tmp/secrets/alerthub-app-secrets
              printf '{"LDAP_APP_ID":"%s","LDAP_APP_PASSWORD":"%s"}' \
                "$LDAP_APP_ID" "$LDAP_APP_PASSWORD" > /tmp/secrets/alerthub-dsldap
              printf '{"IDMS_APP_ID":"%s","IDMS_APP_PASSWORD":"%s"}' \
                "$IDMS_APP_ID" "$IDMS_APP_PASSWORD" > /tmp/secrets/alerthub-hcl
              printf '{"DYNATRACE_API_TOKEN":"%s","CLOUDSTACK_API_KEY":"%s","CLOUDSTACK_SECRET_KEY":"%s"}' \
                "$DYNATRACE_API_TOKEN" "$CLOUDSTACK_API_KEY" "$CLOUDSTACK_SECRET_KEY" \
                > /tmp/secrets/alerthub-infra
              touch /tmp/secrets/alerthub-jfrog /tmp/secrets/alerthub-kubeconfigs
          env:
            - name: DB_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: alerthub-secrets
                  key: DB_PASSWORD
            - name: DB_USER
              valueFrom:
                secretKeyRef:
                  name: alerthub-secrets
                  key: DB_USER
            - name: JWT_SECRET
              valueFrom:
                secretKeyRef:
                  name: alerthub-secrets
                  key: JWT_SECRET
            - name: INTERNAL_SERVICE_TOKEN
              valueFrom:
                secretKeyRef:
                  name: alerthub-secrets
                  key: INTERNAL_SERVICE_TOKEN
            - name: REDIS_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: alerthub-secrets
                  key: REDIS_PASSWORD
            - name: AI_API_KEY
              valueFrom:
                secretKeyRef:
                  name: alerthub-secrets
                  key: AI_API_KEY
            - name: JWT_REFRESH_SECRET
              valueFrom:
                secretKeyRef:
                  name: alerthub-secrets
                  key: JWT_REFRESH_SECRET
            - name: OAUTH_CLIENT_SECRET
              valueFrom:
                secretKeyRef:
                  name: alerthub-secrets
                  key: OAUTH_CLIENT_SECRET
            - name: WEAVIATE_API_KEY
              valueFrom:
                secretKeyRef:
                  name: alerthub-secrets
                  key: weaviate-api-key
            - name: NETAPP_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: alerthub-secrets
                  key: netapp-password
            - name: LDAP_BIND_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: alerthub-secrets
                  key: LDAP_BIND_PASSWORD
            - name: LDAP_APP_ID
              valueFrom:
                secretKeyRef:
                  name: alerthub-dsldap-credentials
                  key: LDAP_APP_ID
            - name: LDAP_APP_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: alerthub-dsldap-credentials
                  key: LDAP_APP_PASSWORD
            - name: IDMS_APP_ID
              valueFrom:
                secretKeyRef:
                  name: alerthub-hcl-credentials
                  key: IDMS_APP_ID
            - name: IDMS_APP_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: alerthub-hcl-credentials
                  key: IDMS_APP_PASSWORD
            - name: DYNATRACE_API_TOKEN
              valueFrom:
                secretKeyRef:
                  name: infrastructure-credentials
                  key: dynatrace-api-token
            - name: CLOUDSTACK_API_KEY
              valueFrom:
                secretKeyRef:
                  name: infrastructure-credentials
                  key: cloudstack-api-key
            - name: CLOUDSTACK_SECRET_KEY
              valueFrom:
                secretKeyRef:
                  name: infrastructure-credentials
                  key: cloudstack-secret-key
          volumeMounts:
            - name: whisper-secrets
              mountPath: /tmp/secrets
        {{- end }}
      containers:
        - name: alerthub-backend
          image: "{{ .Values.images.backend.repository }}:{{ .Values.imageTag | default .Values.images.backend.tag }}"
          imagePullPolicy: {{ .Values.images.backend.pullPolicy }}
          command: ["/bin/sh", "-c"]
          args:
            - |
              ENV=/tmp/secrets/env.sh
              > "$ENV"
              for f in alerthub-app-secrets alerthub-dsldap alerthub-hcl alerthub-infra alerthub-jfrog; do
                [ -f "/tmp/secrets/$f" ] || continue
                awk -F'"' '{
                  for (i=2; i<NF; i+=4) {
                    if ($i ~ /^[A-Z_][A-Z0-9_]*$/ && $(i+2) != "")
                      print "export " $i "=" $(i+2)
                  }
                }' "/tmp/secrets/$f" >> "$ENV"
              done
              mkdir -p /etc/kubeconfigs
              if [ -f /tmp/secrets/alerthub-kubeconfigs ]; then
                awk -F'"' '{
                  for (i=2; i<NF; i+=4) {
                    fname = $i; content = $(i+2)
                    if (fname ~ /\.yaml$/ && content != "") {
                      gsub(/\\n/, "\n", content)
                      print content > ("/etc/kubeconfigs/" fname)
                    }
                  }
                }' /tmp/secrets/alerthub-kubeconfigs 2>/dev/null || true
                cp /tmp/secrets/alerthub-kubeconfigs /etc/kubeconfigs/default.yaml 2>/dev/null || true
              fi
              . /tmp/secrets/env.sh
              export DATABASE_URL="postgresql://${DB_USER}:${DB_PASSWORD}@postgres-primary:5432/${DB_NAME}"
              exec /app/alerthub
          ports:
            - name: http
              containerPort: {{ .Values.backend.port }}
              protocol: TCP
          envFrom:
            - configMapRef:
                name: alerthub-config
          env:
            - name: DSLDAP_ENABLED
              valueFrom:
                configMapKeyRef:
                  name: alerthub-dsldap-config
                  key: DSLDAP_ENABLED
                  optional: true
            - name: DSLDAP_SERVER_URL
              valueFrom:
                configMapKeyRef:
                  name: alerthub-dsldap-config
                  key: DSLDAP_SERVER_URL
                  optional: true
            - name: DSLDAP_USER_SEARCH_BASE
              valueFrom:
                configMapKeyRef:
                  name: alerthub-dsldap-config
                  key: DSLDAP_USER_SEARCH_BASE
                  optional: true
            - name: DSLDAP_CACHE_TTL_MINUTES
              valueFrom:
                configMapKeyRef:
                  name: alerthub-dsldap-config
                  key: DSLDAP_CACHE_TTL_MINUTES
                  optional: true
            - name: DSLDAP_ADMIN_GROUPS
              valueFrom:
                configMapKeyRef:
                  name: alerthub-dsldap-config
                  key: DSLDAP_ADMIN_GROUPS
                  optional: true
            - name: DSLDAP_OPERATOR_GROUPS
              valueFrom:
                configMapKeyRef:
                  name: alerthub-dsldap-config
                  key: DSLDAP_OPERATOR_GROUPS
                  optional: true
            - name: DSLDAP_VIEWER_GROUPS
              valueFrom:
                configMapKeyRef:
                  name: alerthub-dsldap-config
                  key: DSLDAP_VIEWER_GROUPS
                  optional: true
            # Injected via Kubernetes downward API so the LLM enricher builds
            # the correct Ollama service URL for whichever namespace this pod runs in.
            - name: POD_NAMESPACE
              valueFrom:
                fieldRef:
                  fieldPath: metadata.namespace
          volumeMounts:
            - name: whisper-secrets
              mountPath: /tmp/secrets
            - name: kubeconfigs
              mountPath: /etc/kubeconfigs
            {{- if index .Values "whisper" "enabled" | default false }}
            - name: whisper-cert
              mountPath: /etc/ssl/whisper
              readOnly: true
            {{- end }}
          resources:
            {{- toYaml .Values.backend.resources | nindent 12 }}
          securityContext:
            allowPrivilegeEscalation: false
            runAsNonRoot: true
            runAsUser: 1000
            readOnlyRootFilesystem: false
            capabilities:
              drop:
                - ALL
          livenessProbe:
            httpGet:
              path: /health
              port: {{ .Values.backend.port }}
            initialDelaySeconds: 30
            periodSeconds: 10
            timeoutSeconds: 5
            failureThreshold: 3
          readinessProbe:
            httpGet:
              path: /ready
              port: {{ .Values.backend.port }}
            initialDelaySeconds: 15
            periodSeconds: 15
            timeoutSeconds: 10
            failureThreshold: 5
          startupProbe:
            httpGet:
              path: /health
              port: {{ .Values.backend.port }}
            initialDelaySeconds: 10
            periodSeconds: 5
            failureThreshold: 20
      volumes:
        - name: whisper-secrets
          emptyDir: {}
        - name: kubeconfigs
          emptyDir: {}
        {{- if index .Values "whisper" "enabled" | default false }}
        - name: whisper-cert
          csi:
            driver: cmcs.crypto-services.example.com
            readOnly: true
            volumeAttributes:
              cmcs.crypto-services.example.com/duration: "24h"
              cmcs.crypto-services.example.com/fs-group: "1000"
              cmcs.crypto-services.example.com/issuer-kind: AppleCertificateManagerCorpCA
              cmcs.crypto-services.example.com/issuer-name: applecm-whisper-corpca-7915896-7915893
              cmcs.crypto-services.example.com/key-usages: "client auth,digital signature,key encipherment"
        {{- else }}
        - name: whisper-cert
          emptyDir: {}
        {{- end }}
