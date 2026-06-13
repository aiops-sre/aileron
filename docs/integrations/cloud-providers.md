# Cloud Provider Integrations

Aileron natively ingests alerts and discovers topology from every major cloud platform. No code changes are required — configure via environment variables and point each provider's notification mechanism at the corresponding Aileron webhook endpoint.

---

## Table of Contents

- [AWS — CloudWatch and GuardDuty](#aws--cloudwatch-and-guardduty)
- [GCP — Cloud Monitoring and Security Command Center](#gcp--cloud-monitoring-and-security-command-center)
- [Azure — Monitor and Sentinel](#azure--monitor-and-sentinel)
- [AliCloud — Cloud Monitor Service](#alicloud--cloud-monitor-service)

---

## AWS — CloudWatch and GuardDuty

### Step 1 — IAM Policy

Create a least-privilege IAM policy for Aileron's topology discovery. Attach this to an IAM Role (IRSA on EKS) or IAM User (access keys for non-EKS deployments).

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "AileronCloudWatchRead",
      "Effect": "Allow",
      "Action": [
        "cloudwatch:DescribeAlarms",
        "cloudwatch:DescribeAlarmHistory",
        "cloudwatch:GetMetricData",
        "cloudwatch:ListMetrics"
      ],
      "Resource": "*"
    },
    {
      "Sid": "AileronEC2Read",
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeInstances",
        "ec2:DescribeVpcs",
        "ec2:DescribeSubnets",
        "ec2:DescribeSecurityGroups",
        "ec2:DescribeRegions"
      ],
      "Resource": "*"
    },
    {
      "Sid": "AileronEKSRead",
      "Effect": "Allow",
      "Action": [
        "eks:ListClusters",
        "eks:DescribeCluster",
        "eks:ListNodegroups",
        "eks:DescribeNodegroup"
      ],
      "Resource": "*"
    },
    {
      "Sid": "AileronRDSRead",
      "Effect": "Allow",
      "Action": [
        "rds:DescribeDBInstances",
        "rds:DescribeDBClusters",
        "rds:DescribeDBSubnetGroups"
      ],
      "Resource": "*"
    },
    {
      "Sid": "AileronLambdaRead",
      "Effect": "Allow",
      "Action": [
        "lambda:ListFunctions",
        "lambda:GetFunction"
      ],
      "Resource": "*"
    },
    {
      "Sid": "AileronELBRead",
      "Effect": "Allow",
      "Action": [
        "elasticloadbalancing:DescribeLoadBalancers",
        "elasticloadbalancing:DescribeTargetGroups",
        "elasticloadbalancing:DescribeListeners"
      ],
      "Resource": "*"
    },
    {
      "Sid": "AileronGuardDutyRead",
      "Effect": "Allow",
      "Action": [
        "guardduty:ListDetectors",
        "guardduty:ListFindings",
        "guardduty:GetFindings",
        "guardduty:GetDetector"
      ],
      "Resource": "*"
    }
  ]
}
```

**For EKS — IRSA (recommended):**

```bash
eksctl create iamserviceaccount \
  --name aileron-backend \
  --namespace aileron \
  --cluster YOUR_CLUSTER_NAME \
  --attach-policy-arn arn:aws:iam::ACCOUNT_ID:policy/AileronPolicy \
  --approve \
  --region us-east-1
```

**For non-EKS — Access Keys:**

```bash
aws iam create-user --user-name aileron-svc
aws iam attach-user-policy \
  --user-name aileron-svc \
  --policy-arn arn:aws:iam::ACCOUNT_ID:policy/AileronPolicy
aws iam create-access-key --user-name aileron-svc
```

### Step 2 — Environment Variables

```bash
# Enable AWS integration
AILERON_AWS_ENABLED=true
AWS_DEFAULT_REGION=us-east-1

# For access key auth (skip for IRSA)
AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE
AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY

# Comma-separated list of additional regions to discover (optional)
AWS_REGIONS=us-east-1,us-west-2,eu-west-1
```

### Step 3 — CloudWatch Webhook Setup

CloudWatch Alarms send notifications via SNS. Create an SNS topic that forwards to Aileron's HTTP endpoint.

**Create SNS topic and subscription:**

```bash
# Create SNS topic
aws sns create-topic --name aileron-cloudwatch-alerts

# Subscribe Aileron webhook to the topic
aws sns subscribe \
  --topic-arn arn:aws:sns:us-east-1:ACCOUNT_ID:aileron-cloudwatch-alerts \
  --protocol https \
  --notification-endpoint https://aileron.example.com/api/v1/webhooks/cloud/aws/cloudwatch
```

Aileron's endpoint confirms the SNS subscription automatically on first delivery (HTTP 200 with confirmation acknowledgment).

**Point a CloudWatch Alarm at the SNS topic:**

```json
{
  "AlarmName": "HighCPUUtilization",
  "AlarmDescription": "CPU utilization above 80% for 5 minutes",
  "MetricName": "CPUUtilization",
  "Namespace": "AWS/EC2",
  "Statistic": "Average",
  "Period": 300,
  "EvaluationPeriods": 2,
  "Threshold": 80.0,
  "ComparisonOperator": "GreaterThanThreshold",
  "Dimensions": [
    {
      "Name": "InstanceId",
      "Value": "i-0123456789abcdef0"
    }
  ],
  "AlarmActions": [
    "arn:aws:sns:us-east-1:ACCOUNT_ID:aileron-cloudwatch-alerts"
  ],
  "OKActions": [
    "arn:aws:sns:us-east-1:ACCOUNT_ID:aileron-cloudwatch-alerts"
  ]
}
```

Save this as `cw-alarm.json` and apply:

```bash
aws cloudwatch put-metric-alarm --cli-input-json file://cw-alarm.json
```

### Step 4 — GuardDuty EventBridge Rule

Route GuardDuty findings to Aileron via EventBridge:

```bash
# Create EventBridge rule for GuardDuty findings
aws events put-rule \
  --name aileron-guardduty-findings \
  --event-pattern '{
    "source": ["aws.guardduty"],
    "detail-type": ["GuardDuty Finding"]
  }' \
  --state ENABLED

# Create an API Destination to call Aileron webhook
aws events create-connection \
  --name aileron-connection \
  --authorization-type API_KEY \
  --auth-parameters '{
    "ApiKeyAuthParameters": {
      "ApiKeyName": "Authorization",
      "ApiKeyValue": "Bearer YOUR_AILERON_SERVICE_TOKEN"
    }
  }'

aws events create-api-destination \
  --name aileron-guardduty-sink \
  --connection-arn arn:aws:events:us-east-1:ACCOUNT_ID:connection/aileron-connection \
  --invocation-endpoint https://aileron.example.com/api/v1/webhooks/cloud/aws/guardduty \
  --http-method POST

# Add target to the rule
aws events put-targets \
  --rule aileron-guardduty-findings \
  --targets '[{
    "Id": "aileron-target",
    "Arn": "arn:aws:events:us-east-1:ACCOUNT_ID:api-destination/aileron-guardduty-sink",
    "RoleArn": "arn:aws:iam::ACCOUNT_ID:role/AileronEventBridgeRole"
  }]'
```

The same EventBridge approach works for any AWS service. Point the rule's source at `aws.ec2`, `aws.rds`, `aws.ecs`, etc., and use the generic endpoint `POST /api/v1/webhooks/cloud/aws`.

---

## GCP — Cloud Monitoring and Security Command Center

### Step 1 — Service Account Setup

```bash
# Create service account
gcloud iam service-accounts create aileron-sa \
  --display-name="Aileron AIOps Platform" \
  --project=YOUR_PROJECT_ID

# Grant required viewer roles
for ROLE in \
  roles/monitoring.viewer \
  roles/compute.viewer \
  roles/container.viewer \
  roles/cloudsql.viewer \
  roles/storage.objectViewer \
  roles/cloudfunctions.viewer \
  roles/pubsub.viewer; do
  gcloud projects add-iam-policy-binding YOUR_PROJECT_ID \
    --member="serviceAccount:aileron-sa@YOUR_PROJECT_ID.iam.gserviceaccount.com" \
    --role="$ROLE"
done

# For Security Command Center findings
gcloud organizations add-iam-policy-binding YOUR_ORG_ID \
  --member="serviceAccount:aileron-sa@YOUR_PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/securitycenter.findingsViewer"

# Download key (skip for Workload Identity on GKE)
gcloud iam service-accounts keys create aileron-sa-key.json \
  --iam-account=aileron-sa@YOUR_PROJECT_ID.iam.gserviceaccount.com
```

**For GKE — Workload Identity (recommended):**

```bash
# Bind Kubernetes service account to GCP service account
gcloud iam service-accounts add-iam-policy-binding \
  aileron-sa@YOUR_PROJECT_ID.iam.gserviceaccount.com \
  --role=roles/iam.workloadIdentityUser \
  --member="serviceAccount:YOUR_PROJECT_ID.svc.id.goog[aileron/aileron-backend]"

# Annotate the Kubernetes service account
kubectl annotate serviceaccount aileron-backend \
  --namespace aileron \
  iam.gke.io/gcp-service-account=aileron-sa@YOUR_PROJECT_ID.iam.gserviceaccount.com
```

### Step 2 — Environment Variables

```bash
AILERON_GCP_ENABLED=true
GCP_PROJECT_IDS=my-project-id,my-other-project-id

# For key-based auth (skip for Workload Identity)
GOOGLE_APPLICATION_CREDENTIALS=/etc/aileron/gcp-sa-key.json
```

### Step 3 — Cloud Monitoring Notification Channel

Cloud Monitoring sends alerts via Notification Channels. Configure a Webhook channel pointing to Aileron:

```bash
# Create a webhook notification channel
gcloud beta monitoring channels create \
  --display-name="Aileron AIOps" \
  --type=webhook_tokenauth \
  --channel-labels=url=https://aileron.example.com/api/v1/webhooks/cloud/gcp/monitoring \
  --channel-labels=token=YOUR_AILERON_SERVICE_TOKEN
```

Or configure via the Console: **Monitoring → Alerting → Notification Channels → Add New → Webhook**.

**Example alerting policy that fires to Aileron:**

```yaml
# gcp-alert-policy.yaml
displayName: "High CPU on GCE Instance"
documentation:
  content: "GCE instance CPU utilization above 80%"
conditions:
- displayName: "CPU utilization condition"
  conditionThreshold:
    filter: 'resource.type = "gce_instance" AND metric.type = "compute.googleapis.com/instance/cpu/utilization"'
    comparison: COMPARISON_GT
    thresholdValue: 0.8
    duration: 300s
    aggregations:
    - alignmentPeriod: 60s
      perSeriesAligner: ALIGN_MEAN
notificationChannels:
- projects/YOUR_PROJECT_ID/notificationChannels/CHANNEL_ID
alertStrategy:
  autoClose: 1800s
```

Apply with:

```bash
gcloud beta monitoring policies create --policy-from-file=gcp-alert-policy.yaml
```

### Step 4 — Security Command Center Webhook

SCC uses Pub/Sub to deliver findings. Route findings to Aileron via a Pub/Sub push subscription:

```bash
# Create Pub/Sub topic and subscription
gcloud pubsub topics create aileron-scc-findings

# Create a push subscription pointing to Aileron
gcloud pubsub subscriptions create aileron-scc-sub \
  --topic=aileron-scc-findings \
  --push-endpoint=https://aileron.example.com/api/v1/webhooks/cloud/gcp/scc \
  --push-auth-service-account=aileron-sa@YOUR_PROJECT_ID.iam.gserviceaccount.com

# Create a Notification Config in SCC to publish to Pub/Sub
gcloud scc notifications create aileron-findings \
  --organization=YOUR_ORG_ID \
  --description="All SCC findings to Aileron" \
  --pubsub-topic=projects/YOUR_PROJECT_ID/topics/aileron-scc-findings \
  --filter='state = "ACTIVE"'
```

---

## Azure — Monitor and Sentinel

### Step 1 — App Registration

```bash
# Create service principal
az ad sp create-for-rbac \
  --name aileron-aiops \
  --role "Monitoring Reader" \
  --scopes /subscriptions/YOUR_SUBSCRIPTION_ID \
  --sdk-auth > aileron-azure-credentials.json

# Additional role for resource graph queries
az role assignment create \
  --assignee $(az ad sp list --display-name aileron-aiops --query '[0].appId' -o tsv) \
  --role "Reader" \
  --scope /subscriptions/YOUR_SUBSCRIPTION_ID
```

The output of `--sdk-auth` gives you the values for the environment variables below.

**For AKS — Azure Workload Identity (recommended):**

```bash
# Create managed identity
az identity create \
  --name aileron-identity \
  --resource-group YOUR_RG \
  --location eastus

# Assign roles to the identity
IDENTITY_CLIENT_ID=$(az identity show --name aileron-identity \
  --resource-group YOUR_RG --query clientId -o tsv)

az role assignment create \
  --assignee $IDENTITY_CLIENT_ID \
  --role "Monitoring Reader" \
  --scope /subscriptions/YOUR_SUBSCRIPTION_ID

# Federate with the Kubernetes service account
az identity federated-credential create \
  --name aileron-federated \
  --identity-name aileron-identity \
  --resource-group YOUR_RG \
  --issuer $(az aks show --name YOUR_AKS_CLUSTER \
    --resource-group YOUR_RG --query oidcIssuerProfile.issuerUrl -o tsv) \
  --subject system:serviceaccount:aileron:aileron-backend
```

### Step 2 — Environment Variables

```bash
AILERON_AZURE_ENABLED=true
AZURE_SUBSCRIPTION_IDS=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
AZURE_TENANT_ID=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx

# For service principal auth (skip for Workload Identity)
AZURE_CLIENT_ID=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
AZURE_CLIENT_SECRET=your-client-secret
```

### Step 3 — Azure Monitor Action Group

Create an Action Group that calls Aileron's webhook when a metric alert fires:

```bash
# Create action group with webhook action
az monitor action-group create \
  --name aileron-action-group \
  --resource-group YOUR_RG \
  --short-name aileron \
  --action webhook aileron-hook \
    https://aileron.example.com/api/v1/webhooks/cloud/azure/monitor \
    --use-common-alert-schema true
```

**Example metric alert pointing to this action group:**

```bash
az monitor metrics alert create \
  --name "High CPU on VM" \
  --resource-group YOUR_RG \
  --scopes /subscriptions/YOUR_SUBSCRIPTION_ID/resourceGroups/YOUR_RG/providers/Microsoft.Compute/virtualMachines/YOUR_VM \
  --condition "avg Percentage CPU > 80" \
  --window-size 5m \
  --evaluation-frequency 1m \
  --severity 2 \
  --action $(az monitor action-group show \
    --name aileron-action-group \
    --resource-group YOUR_RG \
    --query id -o tsv)
```

The action group payload to Aileron follows the [common alert schema](https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/alerts-common-schema). Aileron's normalizer extracts `essentials.monitorCondition`, `essentials.severity`, and `alertContext` automatically.

### Step 4 — Sentinel Automation Rule

Route Microsoft Sentinel incidents to Aileron for cross-platform correlation:

```bash
# Deploy via ARM template
az deployment group create \
  --resource-group YOUR_RG \
  --template-file sentinel-automation-rule.json
```

`sentinel-automation-rule.json`:

```json
{
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
  "contentVersion": "1.0.0.0",
  "resources": [
    {
      "type": "Microsoft.SecurityInsights/automationRules",
      "apiVersion": "2022-12-01-preview",
      "name": "aileron-sentinel-forward",
      "scope": "[resourceId('Microsoft.OperationalInsights/workspaces', parameters('workspaceName'))]",
      "properties": {
        "displayName": "Forward incidents to Aileron",
        "order": 1,
        "triggeringLogic": {
          "isEnabled": true,
          "triggersOn": "Incidents",
          "triggersWhen": "Created",
          "conditions": [
            {
              "conditionType": "Property",
              "conditionProperties": {
                "propertyName": "IncidentSeverity",
                "operator": "GreaterThan",
                "propertyValues": ["Low"]
              }
            }
          ]
        },
        "actions": [
          {
            "order": 1,
            "actionType": "RunPlaybook",
            "actionConfiguration": {
              "tenantId": "[parameters('tenantId')]",
              "logicAppResourceId": "[resourceId('Microsoft.Logic/workflows', 'aileron-sentinel-forwarder')]"
            }
          }
        ]
      }
    }
  ]
}
```

The Logic App (`aileron-sentinel-forwarder`) calls `POST /api/v1/webhooks/cloud/azure/sentinel` with the Sentinel incident JSON payload. Aileron maps `properties.severity` → incident severity and `properties.title` → alert summary.

---

## AliCloud — Cloud Monitor Service

### Step 1 — RAM User Policy

Create a RAM (Resource Access Management) user with least-privilege read permissions:

```bash
# Using Alibaba Cloud CLI (aliyun)
aliyun ram CreateUser --UserName aileron-svc

aliyun ram CreatePolicy \
  --PolicyName AileronReadPolicy \
  --PolicyDocument '{
    "Version": "1",
    "Statement": [
      {
        "Effect": "Allow",
        "Action": [
          "cms:QueryMetricList",
          "cms:DescribeAlarms",
          "cms:DescribeAlarmHistory",
          "cms:ListAlarmHistory"
        ],
        "Resource": "*"
      },
      {
        "Effect": "Allow",
        "Action": [
          "ecs:DescribeInstances",
          "ecs:DescribeRegions",
          "ecs:DescribeInstanceStatus"
        ],
        "Resource": "*"
      },
      {
        "Effect": "Allow",
        "Action": [
          "cs:DescribeClusters",
          "cs:DescribeCluster"
        ],
        "Resource": "*"
      },
      {
        "Effect": "Allow",
        "Action": [
          "rds:DescribeDBInstances",
          "rds:DescribeRegions"
        ],
        "Resource": "*"
      },
      {
        "Effect": "Allow",
        "Action": [
          "slb:DescribeLoadBalancers"
        ],
        "Resource": "*"
      }
    ]
  }'

aliyun ram AttachPolicyToUser \
  --PolicyName AileronReadPolicy \
  --PolicyType Custom \
  --UserName aileron-svc

# Create access key
aliyun ram CreateAccessKey --UserName aileron-svc
```

### Step 2 — Environment Variables

```bash
AILERON_ALICLOUD_ENABLED=true
ALICLOUD_ACCESS_KEY_ID=LTAI5tExampleKeyId
ALICLOUD_ACCESS_KEY_SECRET=ExampleKeySecret
ALICLOUD_REGION_IDS=cn-hangzhou,cn-beijing,ap-southeast-1
```

### Step 3 — CMS Alarm Webhook Config

Alibaba Cloud Monitor Service supports HTTP callback (webhook) notifications on alarm state changes.

**Via Console:** Cloud Monitor → Alert Rules → Create Alert Rule → select **HTTP Callback** as the notification method.

**Via API:**

```bash
aliyun cms PutResourceMetricRule \
  --RuleId aileron-ecs-cpu-rule \
  --RuleName "ECS High CPU" \
  --Resources '[{"instanceId":"i-bp1example"}]' \
  --Namespace acs_ecs_dashboard \
  --MetricName cpu_total \
  --Period 60 \
  --EscalationsWarn.Statistics Average \
  --EscalationsWarn.ComparisonOperator GreaterThanOrEqualToThreshold \
  --EscalationsWarn.Threshold 80 \
  --EscalationsWarn.Times 3
```

Then create a contact group and alarm notification with HTTP callback:

```bash
# Create alarm notification that calls Aileron
aliyun cms PutContact \
  --ContactName aileron-webhook \
  --Channels.AliIM https://aileron.example.com/api/v1/webhooks/cloud/alicloud

# Note: For HTTP webhooks, use the AlertContact "webhook" channel type.
# In the CMS Console, create an Alert Contact → select "Webhook" →
# set URL to https://aileron.example.com/api/v1/webhooks/cloud/alicloud
```

**Example CMS alarm payload Aileron receives:**

```json
{
  "alertName": "ECS High CPU",
  "alertState": "ALERT",
  "curValue": "92.5",
  "dimensions": "{\"instanceId\":\"i-bp1example\"}",
  "expression": ">=80",
  "instanceName": "prod-web-01",
  "metricName": "cpu_total",
  "namespace": "acs_ecs_dashboard",
  "preTriggerLevel": "WARN",
  "ruleId": "aileron-ecs-cpu-rule",
  "ruleName": "ECS High CPU",
  "timestamp": "1718291234567"
}
```

Aileron's AliCloud normalizer maps `alertState=ALERT` → `status=firing`, `alertState=OK` → `status=resolved`, and extracts `instanceId` from the `dimensions` JSON for topology correlation.
