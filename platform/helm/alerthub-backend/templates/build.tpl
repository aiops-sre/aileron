{{- if index .Values "build" "enabled" | default false }}
apiVersion: batch/v1
kind: Job
metadata:
  name: buildkit-alerthub-backend
  namespace: buildkit
  annotations:
    argocd.argoproj.io/hook: PreSync
    argocd.argoproj.io/hook-delete-policy: BeforeHookCreation
    argocd.argoproj.io/sync-wave: "-1"
spec:
  ttlSecondsAfterFinished: 172800
  activeDeadlineSeconds: 1800
  backoffLimit: 1
  template:
    spec:
      serviceAccountName: buildkit-sa
      imagePullSecrets:
        - name: jfrog-dockers-access
      securityContext:
        sysctls:
          - name: kernel.shm_rmid_forced
            value: "0"
          - name: net.ipv4.ip_unprivileged_port_start
            value: "0"
      hostNetwork: false
      containers:
        - name: buildkit
          image: "moby/buildkit:v0.15.0-rootless"
          imagePullPolicy: IfNotPresent
          securityContext:
            allowPrivilegeEscalation: true
            runAsUser: 1000
            runAsGroup: 1000
            seccompProfile:
              type: Unconfined
            capabilities:
              add: [SYS_ADMIN, SYS_PTRACE, NET_ADMIN, SETFCAP, SETUID, SETGID]
              drop: [ALL]
          command: ["buildctl-daemonless.sh"]
          args:
            - build
            - --frontend=dockerfile.v0
            - --opt=filename=Dockerfile
            - "--opt=context=https://$(GIT_TOKEN)@interactive-git.example.com/interactive-service-delivery/alert-engine.git#{{ .Values.Branch }}"
            - "--output=type=image,name={{ .Values.images.backend.repository }}:{{ .Values.imageTag }},push=true,registry.insecure=true"
            - "--export-cache=type=registry,ref={{ .Values.images.backend.repository }}:buildcache-be,mode=max"
            - "--import-cache=type=registry,ref={{ .Values.images.backend.repository }}:buildcache-be"
            - "--opt=build-arg:GOPROXY=https://proxy.golang.org,direct"
            - "--opt=build-arg:GONOSUMDB=*"
          env:
            - name: DOCKER_CONFIG
              value: /home/user/.docker/
            - name: BUILDKITD_FLAGS
              value: "--oci-worker-no-process-sandbox"
            - name: GIT_TOKEN
              valueFrom:
                secretKeyRef:
                  name: interactive-git-token
                  key: token
          volumeMounts:
            - name: docker-config
              mountPath: /home/user/.docker
      restartPolicy: Never
      volumes:
        - name: docker-config
          secret:
            secretName: jfrog-dockers-access
            items:
              - key: .dockerconfigjson
                path: config.json
{{- end }}
