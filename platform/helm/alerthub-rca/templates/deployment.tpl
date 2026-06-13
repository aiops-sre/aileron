apiVersion: apps/v1
kind: Deployment
metadata:
  name: rca-orchestrator
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "alerthub.labels" . | nindent 4 }}
    app.kubernetes.io/component: rca-orchestrator
    app: rca-orchestrator
spec:
  replicas: 1
  selector:
    matchLabels:
      app: rca-orchestrator
  template:
    metadata:
      labels:
        app: rca-orchestrator
        {{- include "alerthub.selectorLabels" . | nindent 8 }}
        app.kubernetes.io/component: rca-orchestrator
    spec:
      {{- include "alerthub.imagePullSecrets" . | nindent 6 }}
      serviceAccountName: {{ include "alerthub.serviceAccountName" . }}
      terminationGracePeriodSeconds: 30
      securityContext:
        fsGroup: 1000
      initContainers:
        {{- if .Values.secrets_manager.enabled }}
        - name: init-secrets_managerctl
          image: ghcr.io/aileron-platform/crypto-services/secrets_managerctl:1.1.3
          imagePullPolicy: IfNotPresent
          command: ["/bin/sh", "-c"]
          args:
            - |
              set -e
              fetch() {
                ./secrets_managerctl secret fetch \
                  --server secrets_manager.example.com \
                  --client-certificate /etc/ssl/secrets_manager/tls.crt \
                  --client-key /etc/ssl/secrets_manager/tls.key \
                  --client-certificate-format PEM \
                  --namespace aileron-admins \
                  --secret-name "$1" \
                  --output-dir /tmp/secrets
              }
              fetch alerthub-app-secrets
              fetch alerthub-infra
              fetch alerthub-kubeconfigs
          volumeMounts:
            - name: secrets_manager-secrets
              mountPath: /tmp/secrets
            - name: secrets_manager-cert
              mountPath: /etc/ssl/secrets_manager
              readOnly: true
        {{- else }}
        - name: init-k8s-secrets
          image: "{{ .Values.images.redis.repository }}:{{ .Values.images.redis.tag }}"
          imagePullPolicy: IfNotPresent
          command: ["/bin/sh", "-c"]
          args:
            - |
              printf '{"DB_PASSWORD":"%s","DB_USER":"%s","INTERNAL_SERVICE_TOKEN":"%s"}' \
                "$DB_PASSWORD" "$DB_USER" "$INTERNAL_SERVICE_TOKEN" \
                > /tmp/secrets/alerthub-app-secrets
              printf '{"DYNATRACE_API_TOKEN":"%s","CLOUDSTACK_API_KEY":"%s","CLOUDSTACK_SECRET_KEY":"%s"}' \
                "$DYNATRACE_API_TOKEN" "$CLOUDSTACK_API_KEY" "$CLOUDSTACK_SECRET_KEY" \
                > /tmp/secrets/alerthub-infra
              touch /tmp/secrets/alerthub-kubeconfigs
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
            - name: INTERNAL_SERVICE_TOKEN
              valueFrom:
                secretKeyRef:
                  name: alerthub-secrets
                  key: INTERNAL_SERVICE_TOKEN
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
            - name: secrets_manager-secrets
              mountPath: /tmp/secrets
        {{- end }}
      containers:
        - name: rca-orchestrator
          image: "{{ .Values.images.rca.repository }}:{{ .Values.imageTag | default .Values.images.rca.tag }}"
          imagePullPolicy: {{ .Values.images.rca.pullPolicy }}
          command: ["/bin/sh", "-c"]
          args:
            - |
              ENV=/tmp/secrets/env.sh
              > "$ENV"
              for f in alerthub-app-secrets alerthub-infra; do
                [ -f "/tmp/secrets/$f" ] || continue
                awk -F'"' '{for(i=2;i<NF;i+=4) if($i~/^[A-Z_][A-Z0-9_]*$/ && $(i+2)!="") print "export "$i"="$(i+2)}' \
                  "/tmp/secrets/$f" >> "$ENV"
              done
              mkdir -p /etc/kubeconfigs
              if [ -f /tmp/secrets/alerthub-kubeconfigs ]; then
                awk -F'"' '{
                  for(i=2;i<NF;i+=4) {
                    fname=$i; content=$(i+2)
                    if(fname~/\.yaml$/ && content!="") {
                      gsub(/\\n/,"\n",content)
                      print content > ("/etc/kubeconfigs/"fname)
                    }
                  }
                }' /tmp/secrets/alerthub-kubeconfigs 2>/dev/null || true
                cp /tmp/secrets/alerthub-kubeconfigs /etc/kubeconfigs/default.yaml 2>/dev/null || true
              fi
              . /tmp/secrets/env.sh
              export POSTGRES_URL="postgresql://{{ .Values.postgres.user }}:${DB_PASSWORD}@postgres-primary:5432/{{ .Values.postgres.database }}"
              exec uvicorn agent.main:app --host 0.0.0.0 --port 8006 --workers 1
          ports:
            - name: http
              containerPort: 8006
              protocol: TCP
          envFrom:
            - configMapRef:
                name: rca-orchestrator-config
          env:
            - name: ENDOR_DEFAULT_MODEL
              value: "gemini-2.5-flash"
          volumeMounts:
            - name: secrets_manager-secrets
              mountPath: /tmp/secrets
            - name: kubeconfigs
              mountPath: /etc/kubeconfigs
            {{- if .Values.secrets_manager.enabled }}
            - name: secrets_manager-cert
              mountPath: /etc/ssl/secrets_manager
              readOnly: true
            {{- end }}
          resources:
            {{- toYaml .Values.rca.resources | nindent 12 }}
          livenessProbe:
            httpGet:
              path: /health
              port: 8006
            initialDelaySeconds: 30
            periodSeconds: 30
            timeoutSeconds: 10
            failureThreshold: 5
          readinessProbe:
            httpGet:
              path: /health
              port: 8006
            initialDelaySeconds: 20
            periodSeconds: 15
            timeoutSeconds: 10
            failureThreshold: 5
      volumes:
        - name: secrets_manager-secrets
          emptyDir: {}
        - name: kubeconfigs
          emptyDir: {}
        {{- if .Values.secrets_manager.enabled }}
        - name: secrets_manager-cert
          csi:
            driver: cmcs.crypto-services.example.com
            readOnly: true
            volumeAttributes:
              cmcs.crypto-services.example.com/duration: "24h"
              cmcs.crypto-services.example.com/fs-group: "1000"
              cmcs.crypto-services.example.com/issuer-kind: AppleCertificateManagerCorpCA
              cmcs.crypto-services.example.com/issuer-name: applecm-secrets_manager-corpca-7915896-7915893
              cmcs.crypto-services.example.com/key-usages: "client auth,digital signature,key encipherment"
        {{- end }}
