apiVersion: apps/v1
kind: Deployment
metadata:
  name: oie
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "oie.labels" . | nindent 4 }}
    app: oie
spec:
  replicas: {{ .Values.oie.replicas }}
  selector:
    matchLabels:
      {{- include "oie.selectorLabels" . | nindent 6 }}
      app: oie
  template:
    metadata:
      labels:
        app: oie
        {{- include "oie.selectorLabels" . | nindent 8 }}
    spec:
      {{- include "oie.imagePullSecrets" . | nindent 6 }}
      terminationGracePeriodSeconds: 90
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
        {{- end }}
      containers:
        - name: oie
          image: "{{ .Values.images.oie.repository }}:{{ .Values.imageTag | default .Values.images.oie.tag }}"
          imagePullPolicy: {{ .Values.images.oie.pullPolicy }}
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
              fi
              . /tmp/secrets/env.sh
              export OIE_DATABASE_URL="postgresql://{{ .Values.postgres.user }}:${DB_PASSWORD}@postgres-primary:5432/{{ .Values.postgres.database }}?sslmode=disable"
              exec /usr/local/bin/oie
          ports:
            - name: http
              containerPort: 8080
              protocol: TCP
            - name: metrics
              containerPort: 9091
              protocol: TCP
          envFrom:
            - configMapRef:
                name: oie-config
          env:
            - name: POD_NAME
              valueFrom:
                fieldRef:
                  fieldPath: metadata.name
            - name: POD_UID
              valueFrom:
                fieldRef:
                  fieldPath: metadata.uid
            # NetApp password from K8s secret (key uses hyphen, can't be auto-exported by awk)
            - name: NETAPP_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: alerthub-secrets
                  key: netapp-password
                  optional: true
          livenessProbe:
            httpGet:
              path: /healthz
              port: 8080
            initialDelaySeconds: 30
            periodSeconds: 10
            timeoutSeconds: 5
            failureThreshold: 6
          readinessProbe:
            httpGet:
              path: /readyz
              port: 8080
            initialDelaySeconds: 10
            periodSeconds: 5
            timeoutSeconds: 3
            failureThreshold: 3
          resources:
            {{- toYaml .Values.oie.resources | nindent 12 }}
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
