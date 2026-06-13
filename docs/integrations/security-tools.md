# Security Tools Integrations

Aileron acts as a unified sink for CNCF security tools. Runtime detections from Falco, policy violations from Kyverno, and constraint audit findings from OPA/Gatekeeper all become first-class Aileron incidents — correlated against topology and enriched with LLM-generated RCA.

---

## Table of Contents

- [Falco — Runtime Security](#falco--runtime-security)
- [Kyverno — Policy Engine](#kyverno--policy-engine)
- [OPA / Gatekeeper — Constraint Framework](#opa--gatekeeper--constraint-framework)

---

## Falco — Runtime Security

### How It Works

Falco detects anomalous syscall patterns (container escapes, privilege escalation, unexpected network activity) and sends JSON events to Aileron's webhook. Aileron normalizes each Falco event into an `Alert` struct, runs it through the 3-stage pipeline, and correlates it with concurrent infrastructure alerts to surface causal situations.

### Step 1 — falco.yaml Webhook Config

Edit `/etc/falco/falco.yaml` (or your Helm `values.yaml`):

```yaml
# /etc/falco/falco.yaml

# Output format — must be JSON for Aileron
json_output: true
json_include_output_property: true
json_include_tags_property: true

# HTTP output — sends each rule hit to Aileron
http_output:
  enabled: true
  url: https://aileron.example.com/api/v1/webhooks/cncf/falco
  user_agent: "falcosecurity/falco"
  # If Aileron requires auth (set WEBHOOK_REQUIRE_AUTH=true):
  # Add Authorization header via custom_headers (Falco 0.38+)
  # custom_headers:
  #   Authorization: "Bearer YOUR_AILERON_SERVICE_TOKEN"

# Buffered output — batch up to 100 events or flush every 5s
buffered_outputs: false   # set true in high-volume environments

# Severity filter — send NOTICE and above to Aileron
priority: notice
```

**For Helm (falco-charts):**

```yaml
# values.yaml
falco:
  json_output: true
  http_output:
    enabled: true
    url: https://aileron.example.com/api/v1/webhooks/cncf/falco
    user_agent: "falcosecurity/falco"
  priority: notice

falcoctl:
  artifact:
    install:
      refs:
        - falco-rules:3
```

Apply:

```bash
helm upgrade --install falco falcosecurity/falco \
  --namespace falco --create-namespace \
  -f falco-values.yaml
```

### Step 2 — Custom Falco Rule That Triggers Aileron

The following rules detect high-signal security events. Add them to `/etc/falco/rules.d/aileron-rules.yaml`:

```yaml
# /etc/falco/rules.d/aileron-rules.yaml
# Custom Falco rules tuned for Aileron incident creation

# Detect container privilege escalation
- rule: Container Privilege Escalation Detected
  desc: A container process attempted to escalate privileges via setuid/setgid
  condition: >
    spawned_process
    and container
    and proc.vpid != 1
    and (syscall.type = setuid or syscall.type = setgid)
    and not user_known_privilege_escalation
  output: >
    Privilege escalation in container
    (user=%user.name user_uid=%user.uid command=%proc.cmdline
     container=%container.name image=%container.image.repository:%container.image.tag
     namespace=%k8s.ns.name pod=%k8s.pod.name)
  priority: CRITICAL
  tags: [container, privilege_escalation, aileron_high]

# Detect unexpected outbound connections from pods
- rule: Unexpected Outbound Network Connection
  desc: A process in a container established an unexpected outbound TCP connection
  condition: >
    outbound
    and container
    and not trusted_outbound_connection
    and not proc.name in (known_network_processes)
  output: >
    Unexpected outbound connection
    (command=%proc.cmdline connection=%fd.name
     container=%container.name namespace=%k8s.ns.name pod=%k8s.pod.name)
  priority: WARNING
  tags: [network, container, aileron_medium]

# Detect writes to sensitive paths inside containers
- rule: Write to Sensitive Path in Container
  desc: A process wrote to /etc, /bin, /usr/bin, or /sbin inside a container
  condition: >
    open_write
    and container
    and (fd.name startswith /etc
         or fd.name startswith /bin
         or fd.name startswith /usr/bin
         or fd.name startswith /sbin)
    and not package_mgmt_procs
  output: >
    Write to sensitive path in container
    (user=%user.name file=%fd.name command=%proc.cmdline
     container=%container.name namespace=%k8s.ns.name pod=%k8s.pod.name)
  priority: ERROR
  tags: [filesystem, container, aileron_high]

# Detect shell spawned in a container
- rule: Shell Spawned in Container
  desc: A shell was spawned in a container — possible interactive intrusion
  condition: >
    spawned_process
    and container
    and shell_procs
    and not container_entrypoint
  output: >
    Shell spawned in container
    (user=%user.name shell=%proc.name parent=%proc.pname
     container=%container.name image=%container.image.repository:%container.image.tag
     namespace=%k8s.ns.name pod=%k8s.pod.name)
  priority: WARNING
  tags: [shell, container, intrusion, aileron_medium]

# Macro helpers
- macro: trusted_outbound_connection
  condition: >
    fd.sport in (53, 443, 80)
    or fd.sip in (kubernetes_service_ips)

- macro: known_network_processes
  items: [curl, wget, apt, yum, pip, npm]

- macro: user_known_privilege_escalation
  condition: (proc.name in (sudo, su))

- macro: container_entrypoint
  condition: (proc.vpid = 1)
```

### Step 3 — Verify the Integration

```bash
# Trigger a test rule hit (safe — spawns a shell inside a debug pod)
kubectl run falco-test --image=busybox --restart=Never -it --rm \
  --command -- sh -c "echo test"

# Check Aileron received the event
curl -H "Authorization: Bearer YOUR_JWT" \
  https://aileron.example.com/api/v1/incidents?source=falco | jq '.data[] | {id, title, severity}'
```

**Example Aileron incident from Falco:**

| Field | Value |
|---|---|
| `source` | `falco` |
| `title` | `Shell Spawned in Container` |
| `severity` | `warning` |
| `entity_type` | `pod` |
| `entity_id` | `production/app-deployment-7f9b4/app-7f9b4d-xk2pq` |
| `labels.rule` | `Shell Spawned in Container` |
| `labels.container` | `app` |
| `labels.namespace` | `production` |

---

## Kyverno — Policy Engine

### How It Works

Kyverno evaluates admission requests and periodically audits existing resources. When a policy violation is detected (in `Audit` mode — background scan, or `Enforce` mode — blocked admission), Kyverno can fire a notification to Aileron via its `Notification` resource or via an external webhook controller.

The recommended approach is to configure Kyverno's **Policy Report** aggregator with a push bridge (see Step 3), or use a simple CronJob that watches `PolicyReport` CRDs and forwards violations to Aileron.

### Step 1 — Install Kyverno

```bash
helm upgrade --install kyverno kyverno/kyverno \
  --namespace kyverno --create-namespace \
  --set admissionController.replicas=3 \
  --set backgroundController.enabled=true \
  --set cleanupController.enabled=true
```

### Step 2 — ClusterPolicy with Violation Webhook

The following ClusterPolicy enforces resource limits on all containers and uses a Kyverno `generate` rule to emit a ConfigMap violation record that a bridge controller forwards to Aileron:

```yaml
# kyverno-require-limits.yaml
apiVersion: kyverno.io/v1
kind: ClusterPolicy
metadata:
  name: require-resource-limits
  annotations:
    policies.kyverno.io/title: "Require Resource Limits"
    policies.kyverno.io/category: "Resource Management"
    policies.kyverno.io/severity: "medium"
    policies.kyverno.io/description: >
      All containers must specify CPU and memory limits.
      Violations are forwarded to Aileron for incident creation.
spec:
  validationFailureAction: Audit        # change to Enforce to block
  background: true                       # run periodic background scan
  webhookTimeoutSeconds: 10
  rules:
  - name: check-resource-limits
    match:
      any:
      - resources:
          kinds: [Pod]
    validate:
      message: >
        Container '{{ request.object.spec.containers[0].name }}'
        must define resources.limits.cpu and resources.limits.memory.
      foreach:
      - list: "request.object.spec.containers"
        deny:
          conditions:
            any:
            - key: "{{ element.resources.limits.cpu || '' }}"
              operator: Equals
              value: ""
            - key: "{{ element.resources.limits.memory || '' }}"
              operator: Equals
              value: ""
---
# Separate policy: no privileged containers
apiVersion: kyverno.io/v1
kind: ClusterPolicy
metadata:
  name: disallow-privileged-containers
  annotations:
    policies.kyverno.io/title: "Disallow Privileged Containers"
    policies.kyverno.io/category: "Pod Security"
    policies.kyverno.io/severity: "high"
    policies.kyverno.io/description: >
      Privileged containers are disallowed. Violations forwarded to Aileron.
spec:
  validationFailureAction: Enforce
  background: true
  rules:
  - name: privileged-containers
    match:
      any:
      - resources:
          kinds: [Pod]
    validate:
      message: >
        Privileged mode is not allowed.
        Pod: {{ request.object.metadata.name }}
        Namespace: {{ request.object.metadata.namespace }}
      pattern:
        spec:
          =(initContainers):
          - =(securityContext):
              =(privileged): "false"
          containers:
          - =(securityContext):
              =(privileged): "false"
```

### Step 3 — Policy Report Bridge to Aileron

Deploy the Aileron Kyverno bridge — a lightweight controller that watches `PolicyReport` and `ClusterPolicyReport` CRDs and forwards new violations to Aileron:

```yaml
# kyverno-aileron-bridge.yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: kyverno-aileron-bridge
  namespace: kyverno
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kyverno-aileron-bridge
rules:
- apiGroups: [wgpolicyk8s.io, kyverno.io]
  resources: [policyreports, clusterpolicyreports]
  verbs: [get, list, watch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kyverno-aileron-bridge
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: kyverno-aileron-bridge
subjects:
- kind: ServiceAccount
  name: kyverno-aileron-bridge
  namespace: kyverno
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kyverno-aileron-bridge
  namespace: kyverno
  labels:
    app: kyverno-aileron-bridge
spec:
  replicas: 1
  selector:
    matchLabels:
      app: kyverno-aileron-bridge
  template:
    metadata:
      labels:
        app: kyverno-aileron-bridge
    spec:
      serviceAccountName: kyverno-aileron-bridge
      containers:
      - name: bridge
        image: ghcr.io/aiops-sre/aileron-kyverno-bridge:latest
        env:
        - name: AILERON_WEBHOOK_URL
          value: https://aileron.example.com/api/v1/webhooks/cncf/kyverno
        - name: AILERON_SERVICE_TOKEN
          valueFrom:
            secretKeyRef:
              name: aileron-service-token
              key: token
        - name: POLL_INTERVAL_SECONDS
          value: "60"
        - name: MIN_SEVERITY
          value: "medium"   # forward medium, high, critical violations only
        resources:
          limits:
            cpu: 100m
            memory: 128Mi
          requests:
            cpu: 10m
            memory: 32Mi
```

Apply all:

```bash
kubectl apply -f kyverno-require-limits.yaml
kubectl apply -f kyverno-disallow-privileged.yaml
kubectl apply -f kyverno-aileron-bridge.yaml
```

### Step 4 — Set KYVERNO_VIOLATION_WEBHOOK env var

If you use a direct Kyverno webhook notification (Kyverno 1.10+ supports it natively via `Notification` resources), set:

```bash
KYVERNO_VIOLATION_WEBHOOK=https://aileron.example.com/api/v1/webhooks/cncf/kyverno
```

Aileron's Kyverno normalizer handles both the direct webhook payload and the bridge-forwarded `PolicyReport` format.

---

## OPA / Gatekeeper — Constraint Framework

### How It Works

OPA Gatekeeper enforces custom constraints via `ConstraintTemplate` (Rego policies) and `Constraint` (instances with scope + parameters). In `warn` or `dryrun` mode, violations are recorded in the `status.violations` field. Aileron receives these via an audit exporter sidecar or direct webhook call from a custom controller.

### Step 1 — Install Gatekeeper

```bash
helm upgrade --install gatekeeper gatekeeper/gatekeeper \
  --namespace gatekeeper-system --create-namespace \
  --set auditInterval=60 \
  --set constraintViolationsLimit=100 \
  --set emitAdmissionEvents=true \
  --set emitAuditEvents=true
```

### Step 2 — ConstraintTemplate with Aileron Webhook

This template enforces that all Deployments have at least 2 replicas (for HA), and emits violations that the Aileron bridge picks up:

```yaml
# gatekeeper-require-replicas.yaml
apiVersion: templates.gatekeeper.sh/v1
kind: ConstraintTemplate
metadata:
  name: k8srequireminreplicas
  annotations:
    description: "Requires Deployments to have a minimum number of replicas for HA"
spec:
  crd:
    spec:
      names:
        kind: K8sRequireMinReplicas
      validation:
        openAPIV3Schema:
          type: object
          properties:
            minReplicas:
              type: integer
              description: "Minimum number of replicas required"
  targets:
  - target: admission.k8s.gatekeeper.sh
    rego: |
      package k8srequireminreplicas

      violation[{"msg": msg}] {
        input.review.kind.kind == "Deployment"
        replicas := input.review.object.spec.replicas
        replicas < input.parameters.minReplicas
        msg := sprintf(
          "Deployment '%v' in namespace '%v' has %v replica(s), minimum required is %v",
          [
            input.review.object.metadata.name,
            input.review.object.metadata.namespace,
            replicas,
            input.parameters.minReplicas
          ]
        )
      }
---
# Constraint instance: enforce on all namespaces except kube-system
apiVersion: constraints.gatekeeper.sh/v1beta1
kind: K8sRequireMinReplicas
metadata:
  name: require-two-replicas
  annotations:
    aileron.io/severity: "warning"
    aileron.io/category: "reliability"
spec:
  enforcementAction: warn          # warn = record but don't block; use deny to block
  match:
    kinds:
    - apiGroups: [apps]
      kinds: [Deployment]
    excludedNamespaces:
    - kube-system
    - gatekeeper-system
    - cert-manager
    - flux-system
  parameters:
    minReplicas: 2
```

**Additional constraint — no latest image tags:**

```yaml
apiVersion: templates.gatekeeper.sh/v1
kind: ConstraintTemplate
metadata:
  name: k8snolatesttag
spec:
  crd:
    spec:
      names:
        kind: K8sNoLatestTag
  targets:
  - target: admission.k8s.gatekeeper.sh
    rego: |
      package k8snolatesttag

      violation[{"msg": msg}] {
        container := input.review.object.spec.containers[_]
        endswith(container.image, ":latest")
        msg := sprintf(
          "Container '%v' uses ':latest' tag — pin to an explicit digest or version tag",
          [container.name]
        )
      }

      violation[{"msg": msg}] {
        container := input.review.object.spec.containers[_]
        not contains(container.image, ":")
        msg := sprintf(
          "Container '%v' has no image tag — pin to an explicit digest or version tag",
          [container.name]
        )
      }
---
apiVersion: constraints.gatekeeper.sh/v1beta1
kind: K8sNoLatestTag
metadata:
  name: no-latest-tag
  annotations:
    aileron.io/severity: "high"
    aileron.io/category: "supply-chain"
spec:
  enforcementAction: warn
  match:
    kinds:
    - apiGroups: [apps]
      kinds: [Deployment, DaemonSet, StatefulSet]
    excludedNamespaces: [kube-system]
```

### Step 3 — Audit Exporter Config

Deploy the Aileron Gatekeeper audit exporter — it polls `kubectl get constraints -o json` every minute and forwards new violations:

```yaml
# gatekeeper-aileron-exporter.yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: gatekeeper-aileron-exporter
  namespace: gatekeeper-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: gatekeeper-aileron-exporter
rules:
- apiGroups: [constraints.gatekeeper.sh]
  resources: ["*"]
  verbs: [get, list, watch]
- apiGroups: [templates.gatekeeper.sh]
  resources: [constrainttemplates]
  verbs: [get, list, watch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: gatekeeper-aileron-exporter
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: gatekeeper-aileron-exporter
subjects:
- kind: ServiceAccount
  name: gatekeeper-aileron-exporter
  namespace: gatekeeper-system
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: gatekeeper-aileron-exporter
  namespace: gatekeeper-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: gatekeeper-aileron-exporter
  template:
    metadata:
      labels:
        app: gatekeeper-aileron-exporter
    spec:
      serviceAccountName: gatekeeper-aileron-exporter
      containers:
      - name: exporter
        image: ghcr.io/aiops-sre/aileron-gatekeeper-exporter:latest
        env:
        - name: AILERON_WEBHOOK_URL
          value: https://aileron.example.com/api/v1/webhooks/cncf/opa
        - name: AILERON_SERVICE_TOKEN
          valueFrom:
            secretKeyRef:
              name: aileron-service-token
              key: token
        - name: POLL_INTERVAL_SECONDS
          value: "60"
        - name: EMIT_ADMISSION_EVENTS
          value: "true"    # also forward real-time admission webhook violations
        resources:
          limits:
            cpu: 100m
            memory: 64Mi
```

### Step 4 — Verify Violations Reach Aileron

```bash
# Create a single-replica deployment to trigger the constraint
kubectl create deployment test-ha \
  --image=nginx:1.25 \
  --replicas=1 \
  --namespace=default

# Wait for audit cycle (default 60s), then check Aileron
curl -H "Authorization: Bearer YOUR_JWT" \
  "https://aileron.example.com/api/v1/incidents?source=opa&limit=5" | jq '.data[] | {id, title, severity}'

# Expected output:
# {
#   "id": "inc_abc123",
#   "title": "K8sRequireMinReplicas: Deployment 'test-ha' has 1 replica(s)",
#   "severity": "warning"
# }
```

Aileron maps Gatekeeper violations to the entity type `deployment` and extracts namespace + name for topology correlation with any concurrent alerts on the same workload.
