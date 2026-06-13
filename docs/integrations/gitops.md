# GitOps Deployment Guide

Deploy and manage Aileron entirely through GitOps. This guide covers Flux CD (the recommended approach), ArgoCD, image update automation, and multi-cluster deployment patterns.

---

## Table of Contents

- [Flux CD — Bootstrap to Running](#flux-cd--bootstrap-to-running)
- [ArgoCD — Application Manifest](#argocd--application-manifest)
- [Image Update Automation](#image-update-automation)
- [Multi-Cluster Deployment](#multi-cluster-deployment)

---

## Flux CD — Bootstrap to Running

Aileron ships a complete Flux CD deployment manifest at `deploy/flux/aileron-helmrelease.yaml`. This guide walks you from a fresh cluster to a fully running Aileron instance managed entirely by Flux.

### Prerequisites

```bash
# Verify flux CLI is installed
flux version

# Verify cluster access
kubectl cluster-info

# Verify GHCR credentials (needed to pull Aileron images)
kubectl create secret docker-registry ghcr-credentials \
  --docker-server=ghcr.io \
  --docker-username=YOUR_GITHUB_USERNAME \
  --docker-password=YOUR_GITHUB_PAT \
  --namespace aileron
```

### Step 1 — Bootstrap Flux

Bootstrap Flux into your GitOps repository. Flux installs itself and creates the source of truth for your cluster:

```bash
# Bootstrap against a GitHub repository
flux bootstrap github \
  --owner=your-org \
  --repository=your-gitops-repo \
  --branch=main \
  --path=clusters/production \
  --personal \
  --token-auth

# Verify Flux is healthy
flux check

# Expected output:
# ► checking prerequisites
# ✔ Kubernetes 1.29.0 >=1.26.0-0
# ► checking controllers
# ✔ helm-controller: deployment ready
# ✔ kustomize-controller: deployment ready
# ✔ notification-controller: deployment ready
# ✔ source-controller: deployment ready
# ✔ All checks passed
```

**For GitLab:**

```bash
flux bootstrap gitlab \
  --owner=your-group \
  --repository=your-gitops-repo \
  --branch=main \
  --path=clusters/production \
  --token-auth
```

### Step 2 — Create the Aileron Namespace and Secrets

Before applying the HelmRelease, create the namespace and required secrets. These secrets are used by Flux variable substitution (`${VARIABLE_NAME}` syntax in the HelmRelease):

```bash
# Create namespace
kubectl create namespace aileron

# Core secrets — never commit these to Git
kubectl create secret generic aileron-cluster-secrets \
  --namespace flux-system \
  --from-literal=OIDC_PROVIDER_URL=https://keycloak.example.com/realms/aileron \
  --from-literal=OIDC_CLIENT_ID=aileron \
  --from-literal=OIDC_CLIENT_SECRET=your-oidc-secret \
  --from-literal=JWT_SECRET=$(openssl rand -hex 32) \
  --from-literal=JWT_REFRESH_SECRET=$(openssl rand -hex 32) \
  --from-literal=INTERNAL_SERVICE_TOKEN=$(openssl rand -hex 32)

# Non-secret cluster config
kubectl create configmap aileron-cluster-vars \
  --namespace flux-system \
  --from-literal=AILERON_INGRESS_HOST=aileron.example.com \
  --from-literal=AILERON_STORAGE_CLASS=gp3 \
  --from-literal=AILERON_ENVIRONMENT=production
```

**If using External Secrets Operator (recommended):**

```yaml
# aileron-externalsecret.yaml — sync all secrets from Vault
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: aileron-cluster-secrets
  namespace: flux-system
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: vault-backend
    kind: ClusterSecretStore
  target:
    name: aileron-cluster-secrets
    creationPolicy: Owner
  data:
  - secretKey: OIDC_CLIENT_SECRET
    remoteRef:
      key: secret/aileron/oidc
      property: client_secret
  - secretKey: JWT_SECRET
    remoteRef:
      key: secret/aileron/jwt
      property: secret
  - secretKey: JWT_REFRESH_SECRET
    remoteRef:
      key: secret/aileron/jwt
      property: refresh_secret
  - secretKey: INTERNAL_SERVICE_TOKEN
    remoteRef:
      key: secret/aileron/service
      property: internal_token
```

### Step 3 — Apply the HelmRelease

The file `deploy/flux/aileron-helmrelease.yaml` defines:
- `HelmRepository` — points to `oci://ghcr.io/aiops-sre/charts`
- `GitRepository` — tracks the Aileron Git repository (branch: `main`)
- `HelmRelease` — deploys the Aileron Helm chart with health checks and rollback
- `Kustomization` — applies RBAC, NetworkPolicy, and cluster-level config
- `ImageUpdateAutomation` — auto-commits new image tags (see below)

```bash
# Copy the manifest to your GitOps repository
cp deploy/flux/aileron-helmrelease.yaml \
  /path/to/your-gitops-repo/clusters/production/aileron/

# Or apply directly (Flux will sync it automatically after commit)
kubectl apply -f deploy/flux/aileron-helmrelease.yaml

# Watch Flux reconcile
flux get helmreleases --namespace aileron --watch

# Expected output after ~5 minutes:
# NAME     REVISION  SUSPENDED  READY  MESSAGE
# aileron  1.0.0     False      True   Release reconciliation succeeded
```

### Step 4 — Verify the Deployment

```bash
# Check all pods are running
kubectl get pods --namespace aileron

# Check Flux reconciliation status
flux get all --namespace aileron

# Check Aileron health
curl https://aileron.example.com/api/v1/health

# Expected:
# {"status":"healthy","services":{"postgres":"up","redis":"up","kafka":"up","neo4j":"up","ollama":"up"}}
```

### Step 5 — Configure Rollback Strategy

The HelmRelease in `deploy/flux/aileron-helmrelease.yaml` already configures rollback:

```yaml
upgrade:
  remediation:
    retries: 3
    strategy: rollback
rollback:
  timeout: 5m
  cleanupOnFail: true
```

Flux will automatically roll back to the previous release if:
- The Helm upgrade fails
- Health checks (`aileron-backend`, `aileron-dex` Deployments) do not become ready within the timeout

To manually trigger a rollback:

```bash
flux suspend helmrelease aileron --namespace aileron
helm rollback aileron --namespace aileron
flux resume helmrelease aileron --namespace aileron
```

### Step 6 — Receive Flux Events in Aileron

Configure Flux Notification Controller to send reconciliation failures to Aileron. This creates Aileron incidents when GitOps drift or Helm failures occur:

```yaml
# flux-aileron-notification.yaml
apiVersion: notification.toolkit.fluxcd.io/v1beta3
kind: Provider
metadata:
  name: aileron-webhook
  namespace: flux-system
spec:
  type: generic
  address: https://aileron.example.com/api/v1/webhooks/cncf/flux
  secretRef:
    name: aileron-notification-secret   # contains "token" key with service token
---
apiVersion: notification.toolkit.fluxcd.io/v1beta3
kind: Alert
metadata:
  name: aileron-alerts
  namespace: flux-system
spec:
  providerRef:
    name: aileron-webhook
  eventSeverity: error
  eventSources:
  - kind: HelmRelease
    name: "*"
    namespace: aileron
  - kind: Kustomization
    name: "*"
    namespace: flux-system
  - kind: GitRepository
    name: "*"
    namespace: flux-system
  summary: "Flux reconciliation failure"
```

```bash
kubectl apply -f flux-aileron-notification.yaml
```

---

## ArgoCD — Application Manifest

Use this manifest to deploy Aileron via ArgoCD. Apply it to the cluster where ArgoCD is installed (typically your management cluster).

```yaml
# argocd-aileron-app.yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: aileron
  namespace: argocd
  finalizers:
  - resources-finalizer.argocd.io   # enables cascade delete
  annotations:
    # Notification to Aileron when this app drifts or fails
    notifications.argoproj.io/subscribe.on-degraded.aileron: ""
    notifications.argoproj.io/subscribe.on-sync-failed.aileron: ""
    notifications.argoproj.io/subscribe.on-health-degraded.aileron: ""
spec:
  project: default
  source:
    repoURL: https://github.com/aiops-sre/aileron.git
    targetRevision: main
    path: platform/helm/alerthub
    helm:
      releaseName: aileron
      valueFiles:
      - values.yaml
      - values-production.yaml   # environment-specific overrides (in the repo)
      parameters:
      - name: oidc.providerUrl
        value: https://keycloak.example.com/realms/aileron
      - name: certManager.enabled
        value: "true"
      - name: certManager.clusterIssuer
        value: letsencrypt-prod
      - name: ingress.host
        value: aileron.example.com
  destination:
    server: https://kubernetes.default.svc
    namespace: aileron
  syncPolicy:
    automated:
      prune: true          # remove resources deleted from Git
      selfHeal: true       # revert manual changes to cluster
      allowEmpty: false
    syncOptions:
    - CreateNamespace=true
    - PrunePropagationPolicy=foreground
    - RespectIgnoreDifferences=true
    retry:
      limit: 5
      backoff:
        duration: 5s
        factor: 2
        maxDuration: 3m
  ignoreDifferences:
  # ArgoCD-managed fields that Aileron updates at runtime
  - group: apps
    kind: Deployment
    jsonPointers:
    - /spec/replicas    # HPA manages replicas
  health:
    timeout: 300
```

**Apply:**

```bash
# Ensure ArgoCD has RBAC to the aileron namespace
kubectl apply -f argocd-aileron-app.yaml

# Trigger initial sync
argocd app sync aileron

# Watch sync status
argocd app wait aileron --health --timeout 300
```

**ArgoCD Notification to Aileron:**

Configure ArgoCD notifications controller to call Aileron when applications degrade:

```yaml
# argocd-notifications-config.yaml (add to existing ConfigMap)
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-notifications-cm
  namespace: argocd
data:
  service.webhook.aileron: |
    url: https://aileron.example.com/api/v1/webhooks/cloud/aws
    headers:
    - name: Content-Type
      value: application/json
    - name: Authorization
      value: Bearer $aileron-service-token
  template.aileron-sync-failed: |
    webhook:
      aileron:
        method: POST
        body: |
          {
            "alerts": [{
              "status": "firing",
              "labels": {
                "alertname": "ArgoCDSyncFailed",
                "severity": "critical",
                "app": "{{.app.metadata.name}}",
                "namespace": "{{.app.spec.destination.namespace}}"
              },
              "annotations": {
                "summary": "ArgoCD sync failed for {{.app.metadata.name}}",
                "description": "{{.app.status.operationState.message}}"
              }
            }]
          }
  trigger.on-sync-failed: |
    - when: app.status.operationState.phase in ['Error', 'Failed']
      send: [aileron-sync-failed]
```

---

## Image Update Automation

Flux CD's `ImageUpdateAutomation` controller monitors container registries and automatically commits updated image tags to Git. Aileron's `deploy/flux/aileron-helmrelease.yaml` includes the full configuration.

### How It Works

1. Aileron CI pushes a new image to GHCR: `ghcr.io/aiops-sre/aileron-platform:sha-a1b2c3d`
2. Flux `ImageRepository` polls GHCR and detects the new tag
3. `ImagePolicy` selects the latest tag matching `semver:>=1.0.0` (or `glob:sha-*` for SHA tracking)
4. `ImageUpdateAutomation` commits the new tag into your GitOps repo
5. Flux syncs the updated HelmRelease to the cluster
6. Aileron's ArgoCD change correlation links the new deployment to any incidents in the 2-hour window

### Configure Image Policies

```yaml
# image-policies.yaml
---
# Track the platform image
apiVersion: image.toolkit.fluxcd.io/v1beta2
kind: ImageRepository
metadata:
  name: aileron-platform
  namespace: flux-system
spec:
  image: ghcr.io/aiops-sre/aileron-platform
  interval: 5m
  secretRef:
    name: ghcr-credentials   # image pull secret for private registry
---
apiVersion: image.toolkit.fluxcd.io/v1beta2
kind: ImagePolicy
metadata:
  name: aileron-platform
  namespace: flux-system
spec:
  imageRepositoryRef:
    name: aileron-platform
  policy:
    semver:
      range: ">=1.0.0"   # only stable releases
---
# Track the OIE image
apiVersion: image.toolkit.fluxcd.io/v1beta2
kind: ImageRepository
metadata:
  name: aileron-oie
  namespace: flux-system
spec:
  image: ghcr.io/aiops-sre/aileron-oie
  interval: 5m
  secretRef:
    name: ghcr-credentials
---
apiVersion: image.toolkit.fluxcd.io/v1beta2
kind: ImagePolicy
metadata:
  name: aileron-oie
  namespace: flux-system
spec:
  imageRepositoryRef:
    name: aileron-oie
  policy:
    semver:
      range: ">=1.0.0"
```

### Enable Setters in Helm Values

Mark image tags in your values file with Flux setter comments so `ImageUpdateAutomation` knows what to update:

```yaml
# platform/helm/alerthub/values.yaml
images:
  platform:
    repository: ghcr.io/aiops-sre/aileron-platform
    tag: "1.2.3" # {"$imagepolicy": "flux-system:aileron-platform:tag"}
  oie:
    repository: ghcr.io/aiops-sre/aileron-oie
    tag: "1.2.3" # {"$imagepolicy": "flux-system:aileron-oie:tag"}
  agent:
    repository: ghcr.io/aiops-sre/aileron-agent
    tag: "1.2.3" # {"$imagepolicy": "flux-system:aileron-agent:tag"}
```

The `ImageUpdateAutomation` in `deploy/flux/aileron-helmrelease.yaml` uses `strategy: Setters` and `path: ./deploy/flux` to update these values automatically.

---

## Multi-Cluster Deployment

Aileron supports monitoring multiple Kubernetes clusters from a single control plane instance. The KubeSense Agent runs in each target cluster and ships events back to the central Aileron platform.

### Architecture

```
Central Cluster (aileron namespace)
└── aileron-platform (8080)
└── aileron-oie (8081)
└── postgres / redis / kafka / neo4j / ollama

Target Cluster A (aileron-agent namespace)
└── aileron-agent     → Kafka on central cluster
└── aileron-collector → Kafka on central cluster
└── aileron-core      → postgres on central cluster

Target Cluster B (aileron-agent namespace)
└── aileron-agent     → Kafka on central cluster
└── aileron-collector → Kafka on central cluster
└── aileron-core      → postgres on central cluster
```

### Step 1 — Flux Multi-Cluster Bootstrap

Use a centralized GitOps repository with per-cluster paths:

```
gitops-repo/
├── clusters/
│   ├── central/          # aileron platform
│   │   └── aileron-platform.yaml
│   ├── cluster-a/        # agent only
│   │   └── aileron-agent.yaml
│   └── cluster-b/        # agent only
│       └── aileron-agent.yaml
└── infrastructure/
    └── aileron/
        ├── helmrelease-platform.yaml
        └── helmrelease-agent.yaml
```

Bootstrap Flux in each cluster:

```bash
# Central cluster
flux bootstrap github \
  --owner=your-org \
  --repository=gitops-repo \
  --path=clusters/central \
  --context=central-cluster

# Cluster A
flux bootstrap github \
  --owner=your-org \
  --repository=gitops-repo \
  --path=clusters/cluster-a \
  --context=cluster-a

# Cluster B
flux bootstrap github \
  --owner=your-org \
  --repository=gitops-repo \
  --path=clusters/cluster-b \
  --context=cluster-b
```

### Step 2 — Agent HelmRelease for Target Clusters

```yaml
# clusters/cluster-a/aileron-agent.yaml
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: aileron-agent
  namespace: aileron-agent
spec:
  interval: 10m
  chart:
    spec:
      chart: ./agent/helm
      sourceRef:
        kind: GitRepository
        name: aileron-gitops
        namespace: flux-system
  values:
    # Point agent at central cluster's Kafka and Postgres
    kafka:
      brokers: kafka.aileron.central-cluster.example.com:9092
    platform:
      url: https://aileron.central-cluster.example.com
      serviceToken: "${INTERNAL_SERVICE_TOKEN}"
    cluster:
      name: cluster-a
      region: us-east-1
      provider: aws
      environment: production
  valuesFrom:
  - kind: Secret
    name: aileron-agent-secrets
    optional: false
```

### Step 3 — Network Policy for Cross-Cluster Communication

Agent pods in target clusters need to reach Kafka and PostgreSQL on the central cluster. Use a NetworkPolicy or service mesh:

```yaml
# Allow aileron-agent to egress to central cluster
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: aileron-agent-egress
  namespace: aileron-agent
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/part-of: aileron
  policyTypes:
  - Egress
  egress:
  # Allow DNS
  - ports:
    - port: 53
      protocol: UDP
  # Allow Kafka (9092) and PostgreSQL (5432) to central cluster
  - to:
    - ipBlock:
        cidr: 10.0.0.0/8   # central cluster CIDR
    ports:
    - port: 9092    # Kafka
    - port: 5432    # PostgreSQL
    - port: 8080    # aileron-platform API
```

### Step 4 — Verify Multi-Cluster Topology

After agents start up in both target clusters:

```bash
# Check registered clusters
curl -H "Authorization: Bearer YOUR_JWT" \
  https://aileron.example.com/api/v1/kubesense/clusters | jq '.data[].name'

# Expected:
# "cluster-a"
# "cluster-b"

# Check incidents are tagged with cluster name
curl -H "Authorization: Bearer YOUR_JWT" \
  "https://aileron.example.com/api/v1/incidents?limit=5" | \
  jq '.data[] | {id, title, labels.cluster}'
```

Aileron's topology graph in Neo4j will contain nodes from all clusters, allowing cross-cluster blast radius analysis when a shared dependency (database, message queue, CDN) fails.
