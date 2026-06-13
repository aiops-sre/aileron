{{- if index .Values "build" "enabled" | default false }}
apiVersion: batch/v1
kind: Job
metadata:
  name: cosign-alerthub-frontend
  namespace: buildkit
  annotations:
    argocd.argoproj.io/hook: PreSync
    argocd.argoproj.io/hook-delete-policy: BeforeHookCreation
    argocd.argoproj.io/sync-wave: "0"
spec:
  backoffLimit: 0
  template:
    spec:
      imagePullSecrets:
        - name: jfrog-dockers-access
      initContainers:
        - name: init-secrets_manager-client
          image: "ghcr.io/aileron-platform/crypto-services/secrets_managerctl:1.1.3"
          imagePullPolicy: IfNotPresent
          command: ["/bin/sh"]
          args:
            - "-c"
            - |
              ./secrets_managerctl secret fetch \
                --server secrets_manager.example.com \
                --client-certificate /tls-secrets_manager/tls.crt \
                --client-key /tls-secrets_manager/tls.key \
                --client-certificate-format PEM \
                --output-dir /secrets_manager/secrets \
                --buckets cosign \
                --namespace interactive-observability
          volumeMounts:
            - name: secrets
              mountPath: "/secrets_manager/secrets"
            - name: tls-secrets_manager
              mountPath: /tls-secrets_manager
              readOnly: true
      containers:
        - name: cosign
          image: "ghcr.io/aileron-platform/kishore/cosign:v16"
          envFrom:
            - secretRef:
                name: cosign-pass
          command: ["/usr/local/bin/cosign"]
          args:
            - "sign"
            - "--key"
            - "/secrets_manager/secrets/cosign.key"
            - "{{ .Values.images.frontend.repository }}:{{ .Values.imageTag }}"
          volumeMounts:
            - name: secrets
              mountPath: "/secrets_manager/secrets"
            - name: kaniko-secret
              mountPath: /root
      volumes:
        - name: kaniko-secret
          secret:
            secretName: jfrog-dockers-access
            items:
              - key: .dockerconfigjson
                path: .docker/config.json
        - name: secrets
          emptyDir: {}
        - name: tls-secrets_manager
          csi:
            driver: cmcs.crypto-services.example.com
            readOnly: true
            volumeAttributes:
              cmcs.crypto-services.example.com/fs-group: "65532"
              cmcs.crypto-services.example.com/issuer-kind: AppleCertificateManagerCorpCA
              cmcs.crypto-services.example.com/issuer-name: applecm-secrets_manager-corpca-8981099-8981085
              cmcs.crypto-services.example.com/key-usages: client auth,digital signature,key encipherment
      restartPolicy: Never
{{- end }}
