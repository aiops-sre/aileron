# Secrets Management Guide

Aileron's Helm chart is designed to never require secrets stored in Git. This guide covers every secrets management approach from GitOps-safe production setups to manual creation for local development.

---

## Table of Contents

- [External Secrets Operator](#external-secrets-operator)
  - [HashiCorp Vault](#hashicorp-vault)
  - [AWS Secrets Manager](#aws-secrets-manager)
  - [GCP Secret Manager](#gcp-secret-manager)
  - [Azure Key Vault](#azure-key-vault)
- [Sealed Secrets](#sealed-secrets)
- [Vault Agent Injector](#vault-agent-injector)
- [Plain Kubernetes Secrets (Dev Only)](#plain-kubernetes-secrets-dev-only)

---

## External Secrets Operator

[External Secrets Operator (ESO)](https://external-secrets.io) syncs secrets from external backends into Kubernetes `Secret` objects on a schedule. Aileron's Helm chart creates `ExternalSecret` resources when `externalSecrets.enabled=true`.

### Install ESO

```bash
helm upgrade --install external-secrets external-secrets/external-secrets \
  --namespace external-secrets --create-namespace \
  --set installCRDs=true \
  --set webhook.port=9443
```

---

### HashiCorp Vault

#### Step 1 — Configure Vault

```bash
# Enable KV v2 secrets engine
vault secrets enable -path=secret kv-v2

# Write Aileron secrets
vault kv put secret/aileron/core \
  jwt_secret=$(openssl rand -hex 32) \
  jwt_refresh_secret=$(openssl rand -hex 32) \
  internal_service_token=$(openssl rand -hex 32)

vault kv put secret/aileron/oidc \
  client_id=aileron \
  client_secret=YOUR_OIDC_SECRET \
  provider_url=https://keycloak.example.com/realms/aileron

vault kv put secret/aileron/database \
  url=postgres://aileron:password@postgres.aileron.svc.cluster.local:5432/aileron?sslmode=disable

vault kv put secret/aileron/redis \
  addr=redis.aileron.svc.cluster.local:6379

# Enable Kubernetes auth method
vault auth enable kubernetes

# Configure Kubernetes auth to use the cluster's service account JWT
vault write auth/kubernetes/config \
  kubernetes_host="https://$KUBERNETES_PORT_443_TCP_ADDR:443" \
  token_reviewer_jwt="$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)" \
  kubernetes_ca_cert=@/var/run/secrets/kubernetes.io/serviceaccount/ca.crt

# Create a policy for Aileron
vault policy write aileron - <<EOF
path "secret/data/aileron/*" {
  capabilities = ["read"]
}
EOF

# Create a role binding the Kubernetes service account to the policy
vault write auth/kubernetes/role/aileron \
  bound_service_account_names=aileron-backend,aileron-oie \
  bound_service_account_namespaces=aileron \
  policies=aileron \
  ttl=1h
```

#### Step 2 — ClusterSecretStore for Vault

```yaml
# vault-cluster-secret-store.yaml
apiVersion: external-secrets.io/v1beta1
kind: ClusterSecretStore
metadata:
  name: vault-backend
spec:
  provider:
    vault:
      server: https://vault.example.com
      path: secret
      version: v2
      auth:
        kubernetes:
          mountPath: kubernetes
          role: aileron
          serviceAccountRef:
            name: aileron-backend
            namespace: aileron
```

Apply:

```bash
kubectl apply -f vault-cluster-secret-store.yaml

# Verify the store is ready
kubectl get clustersecretstore vault-backend
# Expected: STATUS = Valid
```

#### Step 3 — ExternalSecret for Aileron Core Secrets

```yaml
# aileron-externalsecret-vault.yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: aileron-core-secrets
  namespace: aileron
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: vault-backend
    kind: ClusterSecretStore
  target:
    name: aileron-core-secrets       # name of the K8s Secret to create/update
    creationPolicy: Owner
    deletionPolicy: Retain           # keep K8s secret if ExternalSecret is deleted
    template:
      type: Opaque
      engineVersion: v2
  data:
  - secretKey: JWT_SECRET
    remoteRef:
      key: aileron/core
      property: jwt_secret
  - secretKey: JWT_REFRESH_SECRET
    remoteRef:
      key: aileron/core
      property: jwt_refresh_secret
  - secretKey: INTERNAL_SERVICE_TOKEN
    remoteRef:
      key: aileron/core
      property: internal_service_token
  - secretKey: DATABASE_URL
    remoteRef:
      key: aileron/database
      property: url
  - secretKey: REDIS_ADDR
    remoteRef:
      key: aileron/redis
      property: addr
---
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: aileron-oidc-secrets
  namespace: aileron
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: vault-backend
    kind: ClusterSecretStore
  target:
    name: aileron-oidc-secrets
    creationPolicy: Owner
    deletionPolicy: Retain
  data:
  - secretKey: OIDC_CLIENT_ID
    remoteRef:
      key: aileron/oidc
      property: client_id
  - secretKey: OIDC_CLIENT_SECRET
    remoteRef:
      key: aileron/oidc
      property: client_secret
  - secretKey: OIDC_PROVIDER_URL
    remoteRef:
      key: aileron/oidc
      property: provider_url
```

Apply and verify:

```bash
kubectl apply -f aileron-externalsecret-vault.yaml

# Check sync status
kubectl get externalsecret aileron-core-secrets -n aileron
# Expected: READY = True, STATUS = SecretSynced

# Verify the K8s Secret was created
kubectl get secret aileron-core-secrets -n aileron
```

#### Step 4 — Reference Secrets in Helm

```bash
helm upgrade --install aileron ./platform/helm \
  --namespace aileron \
  --set externalSecrets.enabled=true \
  --set externalSecrets.secretStore=vault-backend \
  --set externalSecrets.secretStorKind=ClusterSecretStore
```

---

### AWS Secrets Manager

#### Step 1 — Create Secrets in AWS

```bash
aws secretsmanager create-secret \
  --name /aileron/core \
  --secret-string '{
    "jwt_secret": "'$(openssl rand -hex 32)'",
    "jwt_refresh_secret": "'$(openssl rand -hex 32)'",
    "internal_service_token": "'$(openssl rand -hex 32)'"
  }'

aws secretsmanager create-secret \
  --name /aileron/oidc \
  --secret-string '{
    "client_id": "aileron",
    "client_secret": "YOUR_OIDC_SECRET",
    "provider_url": "https://keycloak.example.com/realms/aileron"
  }'

aws secretsmanager create-secret \
  --name /aileron/database \
  --secret-string '{"url": "postgres://aileron:PASSWORD@RDS_HOST:5432/aileron"}'
```

#### Step 2 — IAM Policy for ESO

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "secretsmanager:GetSecretValue",
        "secretsmanager:DescribeSecret"
      ],
      "Resource": "arn:aws:secretsmanager:us-east-1:ACCOUNT_ID:secret:/aileron/*"
    }
  ]
}
```

Attach to the IRSA role for the `external-secrets` service account:

```bash
eksctl create iamserviceaccount \
  --name external-secrets \
  --namespace external-secrets \
  --cluster YOUR_CLUSTER \
  --attach-policy-arn arn:aws:iam::ACCOUNT_ID:policy/AileronSecretsManagerPolicy \
  --approve
```

#### Step 3 — ClusterSecretStore for AWS

```yaml
# aws-cluster-secret-store.yaml
apiVersion: external-secrets.io/v1beta1
kind: ClusterSecretStore
metadata:
  name: aws-secrets-manager
spec:
  provider:
    aws:
      service: SecretsManager
      region: us-east-1
      auth:
        jwt:
          serviceAccountRef:
            name: external-secrets
            namespace: external-secrets
```

#### Step 4 — ExternalSecret for AWS

```yaml
# aileron-externalsecret-aws.yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: aileron-core-secrets
  namespace: aileron
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: aws-secrets-manager
    kind: ClusterSecretStore
  target:
    name: aileron-core-secrets
    creationPolicy: Owner
  dataFrom:
  - extract:
      key: /aileron/core     # extracts all JSON properties as secret keys
---
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: aileron-oidc-secrets
  namespace: aileron
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: aws-secrets-manager
    kind: ClusterSecretStore
  target:
    name: aileron-oidc-secrets
    creationPolicy: Owner
  dataFrom:
  - extract:
      key: /aileron/oidc
---
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: aileron-database-secret
  namespace: aileron
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: aws-secrets-manager
    kind: ClusterSecretStore
  target:
    name: aileron-database-secret
    creationPolicy: Owner
  dataFrom:
  - extract:
      key: /aileron/database
```

---

### GCP Secret Manager

#### Step 1 — Create Secrets in GCP

```bash
# Core secrets
echo -n $(openssl rand -hex 32) | \
  gcloud secrets create aileron-jwt-secret --data-file=- --project=YOUR_PROJECT

echo -n $(openssl rand -hex 32) | \
  gcloud secrets create aileron-jwt-refresh-secret --data-file=- --project=YOUR_PROJECT

echo -n $(openssl rand -hex 32) | \
  gcloud secrets create aileron-internal-service-token --data-file=- --project=YOUR_PROJECT

echo -n "YOUR_OIDC_SECRET" | \
  gcloud secrets create aileron-oidc-client-secret --data-file=- --project=YOUR_PROJECT
```

#### Step 2 — Grant ESO Service Account Access

```bash
ESO_SA=external-secrets@YOUR_PROJECT.iam.gserviceaccount.com

for SECRET in aileron-jwt-secret aileron-jwt-refresh-secret \
              aileron-internal-service-token aileron-oidc-client-secret; do
  gcloud secrets add-iam-policy-binding $SECRET \
    --member="serviceAccount:$ESO_SA" \
    --role="roles/secretmanager.secretAccessor" \
    --project=YOUR_PROJECT
done
```

#### Step 3 — ClusterSecretStore for GCP

```yaml
# gcp-cluster-secret-store.yaml
apiVersion: external-secrets.io/v1beta1
kind: ClusterSecretStore
metadata:
  name: gcp-secret-manager
spec:
  provider:
    gcpsm:
      projectID: YOUR_PROJECT_ID
      auth:
        workloadIdentity:
          clusterLocation: us-central1
          clusterName: YOUR_GKE_CLUSTER
          clusterProjectID: YOUR_PROJECT_ID
          serviceAccountRef:
            name: external-secrets
            namespace: external-secrets
```

#### Step 4 — ExternalSecret for GCP

```yaml
# aileron-externalsecret-gcp.yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: aileron-core-secrets
  namespace: aileron
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: gcp-secret-manager
    kind: ClusterSecretStore
  target:
    name: aileron-core-secrets
    creationPolicy: Owner
  data:
  - secretKey: JWT_SECRET
    remoteRef:
      key: aileron-jwt-secret
  - secretKey: JWT_REFRESH_SECRET
    remoteRef:
      key: aileron-jwt-refresh-secret
  - secretKey: INTERNAL_SERVICE_TOKEN
    remoteRef:
      key: aileron-internal-service-token
---
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: aileron-oidc-secrets
  namespace: aileron
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: gcp-secret-manager
    kind: ClusterSecretStore
  target:
    name: aileron-oidc-secrets
    creationPolicy: Owner
  data:
  - secretKey: OIDC_CLIENT_SECRET
    remoteRef:
      key: aileron-oidc-client-secret
```

---

### Azure Key Vault

#### Step 1 — Create Secrets in Azure Key Vault

```bash
VAULT_NAME=aileron-kv
RG=your-resource-group

az keyvault create --name $VAULT_NAME --resource-group $RG --location eastus

az keyvault secret set --vault-name $VAULT_NAME \
  --name jwt-secret --value $(openssl rand -hex 32)

az keyvault secret set --vault-name $VAULT_NAME \
  --name jwt-refresh-secret --value $(openssl rand -hex 32)

az keyvault secret set --vault-name $VAULT_NAME \
  --name internal-service-token --value $(openssl rand -hex 32)

az keyvault secret set --vault-name $VAULT_NAME \
  --name oidc-client-secret --value "YOUR_OIDC_SECRET"
```

#### Step 2 — Grant ESO Access

```bash
ESO_CLIENT_ID=$(az identity show --name external-secrets-identity \
  --resource-group $RG --query clientId -o tsv)

az keyvault set-policy --name $VAULT_NAME \
  --resource-group $RG \
  --spn $ESO_CLIENT_ID \
  --secret-permissions get list
```

#### Step 3 — ClusterSecretStore for Azure

```yaml
# azure-cluster-secret-store.yaml
apiVersion: external-secrets.io/v1beta1
kind: ClusterSecretStore
metadata:
  name: azure-key-vault
spec:
  provider:
    azurekv:
      tenantId: YOUR_TENANT_ID
      vaultUrl: https://aileron-kv.vault.azure.net
      authType: WorkloadIdentity
      serviceAccountRef:
        name: external-secrets
        namespace: external-secrets
```

#### Step 4 — ExternalSecret for Azure

```yaml
# aileron-externalsecret-azure.yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: aileron-core-secrets
  namespace: aileron
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: azure-key-vault
    kind: ClusterSecretStore
  target:
    name: aileron-core-secrets
    creationPolicy: Owner
  data:
  - secretKey: JWT_SECRET
    remoteRef:
      key: jwt-secret        # Azure KV secret name
  - secretKey: JWT_REFRESH_SECRET
    remoteRef:
      key: jwt-refresh-secret
  - secretKey: INTERNAL_SERVICE_TOKEN
    remoteRef:
      key: internal-service-token
---
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: aileron-oidc-secrets
  namespace: aileron
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: azure-key-vault
    kind: ClusterSecretStore
  target:
    name: aileron-oidc-secrets
    creationPolicy: Owner
  data:
  - secretKey: OIDC_CLIENT_SECRET
    remoteRef:
      key: oidc-client-secret
```

---

## Sealed Secrets

[Sealed Secrets](https://github.com/bitnami-labs/sealed-secrets) encrypts Kubernetes secrets using a cluster-specific key, making it safe to commit the sealed form to Git. Only the controller running in your cluster can decrypt them.

### Install Sealed Secrets Controller

```bash
helm upgrade --install sealed-secrets sealed-secrets/sealed-secrets \
  --namespace kube-system \
  --set fullnameOverride=sealed-secrets-controller
```

### Encrypt and Commit Aileron Secrets

```bash
# Install kubeseal CLI
brew install kubeseal   # macOS
# or: curl -sL https://github.com/bitnami-labs/sealed-secrets/releases/latest/download/kubeseal-linux-amd64 -o /usr/local/bin/kubeseal

# Fetch the cluster's public key (do this once per cluster)
kubeseal --fetch-cert \
  --controller-name=sealed-secrets-controller \
  --controller-namespace=kube-system > sealed-secrets-cert.pem

# Create a regular K8s secret (do NOT apply it — pipe straight to kubeseal)
kubectl create secret generic aileron-core-secrets \
  --namespace aileron \
  --from-literal=JWT_SECRET=$(openssl rand -hex 32) \
  --from-literal=JWT_REFRESH_SECRET=$(openssl rand -hex 32) \
  --from-literal=INTERNAL_SERVICE_TOKEN=$(openssl rand -hex 32) \
  --dry-run=client \
  -o yaml | \
kubeseal \
  --cert sealed-secrets-cert.pem \
  --format yaml > deploy/k8s/aileron-sealed-core-secrets.yaml

# Encrypt OIDC secrets
kubectl create secret generic aileron-oidc-secrets \
  --namespace aileron \
  --from-literal=OIDC_CLIENT_ID=aileron \
  --from-literal=OIDC_CLIENT_SECRET=YOUR_OIDC_SECRET \
  --from-literal=OIDC_PROVIDER_URL=https://keycloak.example.com/realms/aileron \
  --dry-run=client \
  -o yaml | \
kubeseal \
  --cert sealed-secrets-cert.pem \
  --format yaml > deploy/k8s/aileron-sealed-oidc-secrets.yaml
```

**The generated `SealedSecret` YAML is safe to commit to Git:**

```yaml
# deploy/k8s/aileron-sealed-core-secrets.yaml (safe to commit)
apiVersion: bitnami.com/v1alpha1
kind: SealedSecret
metadata:
  name: aileron-core-secrets
  namespace: aileron
spec:
  encryptedData:
    JWT_SECRET: AgBy3i4OJSWK+PiTySYZZA9rO43cGDEq...   # encrypted blob
    JWT_REFRESH_SECRET: AgCH4Jh...
    INTERNAL_SERVICE_TOKEN: AgDk7Mq...
  template:
    metadata:
      name: aileron-core-secrets
      namespace: aileron
```

Apply to cluster (Flux or ArgoCD will do this automatically if the file is in the GitOps path):

```bash
kubectl apply -f deploy/k8s/aileron-sealed-core-secrets.yaml
kubectl apply -f deploy/k8s/aileron-sealed-oidc-secrets.yaml

# Verify the controller decrypted and created the plain Secret
kubectl get secret aileron-core-secrets -n aileron
```

### Rotate Sealed Secrets

```bash
# Re-generate values and re-seal
kubectl create secret generic aileron-core-secrets \
  --namespace aileron \
  --from-literal=JWT_SECRET=$(openssl rand -hex 32) \
  --from-literal=JWT_REFRESH_SECRET=$(openssl rand -hex 32) \
  --from-literal=INTERNAL_SERVICE_TOKEN=$(openssl rand -hex 32) \
  --dry-run=client \
  -o yaml | \
kubeseal --cert sealed-secrets-cert.pem --format yaml \
  > deploy/k8s/aileron-sealed-core-secrets.yaml

# Commit and push — Flux/ArgoCD will apply automatically
git add deploy/k8s/aileron-sealed-core-secrets.yaml
git commit -m "chore(secrets): rotate aileron core secrets"
git push
```

---

## Vault Agent Injector

If you run HashiCorp Vault and prefer the sidecar injection approach over ESO (which creates K8s Secret objects), use the Vault Agent Injector. The injector writes secrets directly to a tmpfs volume in your pod — they never appear as K8s Secrets.

### Prerequisites

```bash
# Install Vault Agent Injector (ships with the Vault Helm chart)
helm upgrade --install vault hashicorp/vault \
  --namespace vault --create-namespace \
  --set "injector.enabled=true" \
  --set "server.dev.enabled=false"
```

Ensure the Vault Kubernetes auth method is configured (see [HashiCorp Vault setup](#hashicorp-vault) Step 1 above).

### Annotate Aileron Pods

Add annotations to the Aileron backend pod template. The injector reads these and injects an init container that writes secrets to `/vault/secrets/`:

```yaml
# Add to platform/helm/alerthub/templates/deployment.yaml pod template
# (or override via values.yaml podAnnotations)
podAnnotations:
  vault.hashicorp.com/agent-inject: "true"
  vault.hashicorp.com/role: "aileron"
  vault.hashicorp.com/agent-inject-secret-core: "secret/data/aileron/core"
  vault.hashicorp.com/agent-inject-template-core: |
    {{- with secret "secret/data/aileron/core" -}}
    export JWT_SECRET="{{ .Data.data.jwt_secret }}"
    export JWT_REFRESH_SECRET="{{ .Data.data.jwt_refresh_secret }}"
    export INTERNAL_SERVICE_TOKEN="{{ .Data.data.internal_service_token }}"
    {{- end }}
  vault.hashicorp.com/agent-inject-secret-oidc: "secret/data/aileron/oidc"
  vault.hashicorp.com/agent-inject-template-oidc: |
    {{- with secret "secret/data/aileron/oidc" -}}
    export OIDC_CLIENT_ID="{{ .Data.data.client_id }}"
    export OIDC_CLIENT_SECRET="{{ .Data.data.client_secret }}"
    export OIDC_PROVIDER_URL="{{ .Data.data.provider_url }}"
    {{- end }}
  vault.hashicorp.com/agent-inject-secret-db: "secret/data/aileron/database"
  vault.hashicorp.com/agent-inject-template-db: |
    {{- with secret "secret/data/aileron/database" -}}
    export DATABASE_URL="{{ .Data.data.url }}"
    {{- end }}
```

Update the Aileron container entrypoint to source these files:

```yaml
# In the container spec
command: ["/bin/sh", "-c"]
args:
- |
  source /vault/secrets/core
  source /vault/secrets/oidc
  source /vault/secrets/db
  exec /aileron-platform
```

Or use the `vault.hashicorp.com/agent-inject-command` annotation to run a command after the file is written.

### Verify Injection

```bash
# Check the init container ran successfully
kubectl describe pod -n aileron -l app=aileron-backend | grep -A5 "Init Containers"

# Check the secret file was written (tmpfs — not persisted to disk)
kubectl exec -n aileron deploy/aileron-backend -c aileron-backend \
  -- ls -la /vault/secrets/
```

---

## Plain Kubernetes Secrets (Dev Only)

For local development with Docker Compose or a dev cluster. **Never use this approach in production or commit these files to Git.**

### Create Secrets Manually

```bash
# Generate secure random values
JWT_SECRET=$(openssl rand -hex 32)
JWT_REFRESH_SECRET=$(openssl rand -hex 32)
INTERNAL_TOKEN=$(openssl rand -hex 32)

# Create the secrets
kubectl create namespace aileron --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic aileron-core-secrets \
  --namespace aileron \
  --from-literal=JWT_SECRET=$JWT_SECRET \
  --from-literal=JWT_REFRESH_SECRET=$JWT_REFRESH_SECRET \
  --from-literal=INTERNAL_SERVICE_TOKEN=$INTERNAL_TOKEN \
  --from-literal=DATABASE_URL=postgres://aileron:aileron@postgres.aileron.svc.cluster.local:5432/aileron?sslmode=disable \
  --from-literal=REDIS_ADDR=redis.aileron.svc.cluster.local:6379

kubectl create secret generic aileron-oidc-secrets \
  --namespace aileron \
  --from-literal=OIDC_CLIENT_ID=aileron \
  --from-literal=OIDC_CLIENT_SECRET=dev-secret \
  --from-literal=OIDC_PROVIDER_URL=http://dex.aileron.svc.cluster.local:5556

kubectl create secret generic aileron-kafka-secret \
  --namespace aileron \
  --from-literal=KAFKA_BROKERS=kafka.aileron.svc.cluster.local:9092

# Verify
kubectl get secrets -n aileron
```

### Reference in Helm (values override)

```yaml
# values-dev.yaml — override existingSecrets to use manually created secrets
existingSecrets:
  core: aileron-core-secrets
  oidc: aileron-oidc-secrets
  kafka: aileron-kafka-secret
```

```bash
helm upgrade --install aileron ./platform/helm \
  --namespace aileron \
  -f platform/helm/alerthub/values.yaml \
  -f values-dev.yaml
```

### Docker Compose (Local Dev)

For Docker Compose, create `platform/.env` from the example and fill in values:

```bash
cp platform/.env.example platform/.env

# Edit platform/.env:
# JWT_SECRET=<run: openssl rand -hex 32>
# JWT_REFRESH_SECRET=<run: openssl rand -hex 32>
# INTERNAL_SERVICE_TOKEN=<run: openssl rand -hex 32>
# OIDC_CLIENT_SECRET=<from your dev IdP>
# OIDC_PROVIDER_URL=http://localhost:5556   (local Dex)
```

The `platform/.env` file is in `.gitignore` and is never committed. If you accidentally stage it:

```bash
git rm --cached platform/.env
echo "platform/.env" >> .gitignore
```
