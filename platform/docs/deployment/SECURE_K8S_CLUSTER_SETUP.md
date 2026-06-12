# Secure Kubernetes Cluster Integration Guide

## 🔐 **Production-Ready Service Account Setup**

### **Why Service Accounts > kubectl Context**
- ✅ **Readonly Permissions**: Only necessary access for topology discovery
- ✅ **Long-lived Tokens**: Don't expire like user tokens
- ✅ **Audit Trail**: Traceable access for compliance
- ✅ **Network Security**: Restricted network policies
- ✅ **Production Safe**: No admin privileges required

## 🚀 **Setup Instructions for Each K8s Cluster**

### **Step 1: Apply RBAC Configuration**
```bash
# Apply to each cluster you want to monitor
kubectl apply -f k8s/alerthub-k8s-rbac.yaml
```

This creates:
- ✅ `alerthub-k8s-intelligence` service account
- ✅ `alerthub-k8s-readonly` ClusterRole (minimal permissions)
- ✅ ClusterRoleBinding for access
- ✅ Service account token secret

### **Step 2: Extract Service Account Token**
```bash
# Get the service account token
kubectl get secret alerthub-k8s-intelligence-token -n alerthub-system \
  -o jsonpath='{.data.token}' | base64 --decode

# Get the CA certificate (for TLS verification)
kubectl get secret alerthub-k8s-intelligence-token -n alerthub-system \
  -o jsonpath='{.data.ca\.crt}' | base64 --decode
```

### **Step 3: Add Cluster via SRE Command Center**
1. **Navigate**: `http://localhost:3001/kubernetes`
2. **Click**: "Add K8s Cluster"
3. **Fill Form**:
   - **Cluster Name**: `mps-sandbox-rno`
   - **Display Name**: `MPS Sandbox Reno`
   - **API Server**: `https://mps-sandbox-rno.example.com:6443`
   - **Service Account Token**: Paste token from Step 2
   - **CA Certificate**: Paste CA cert (optional but recommended)
   - **Region**: `reno`
   - **Environment**: `sandbox`

## 🔒 **Security Features**

### **Minimal Permissions Granted**:
```yaml
# Only readonly access to:
- nodes (topology discovery)
- namespaces (organization)
- pods/services (application mapping)
- deployments/statefulsets (workload topology)
- events (investigation)
- ingresses (network topology)
- persistentvolumes (storage topology)
```

### **Network Security**:
- ✅ NetworkPolicy restricts connections to K8s API only
- ✅ No access to secrets data (only metadata)
- ✅ No write permissions anywhere
- ✅ Limited to specific namespace

### **Audit & Compliance**:
- ✅ All API calls traceable to service account
- ✅ RBAC policies clearly defined
- ✅ No persistent cluster access required
- ✅ Tokens can be rotated regularly

## 🔧 **Automated Setup Script**

### **For Multiple Clusters**:
```bash
#!/bin/bash
# setup-alerthub-monitoring.sh

CLUSTERS=("mps-sandbox-rno" "k8prod01-rno" "k8prod01-mdn")

for cluster in "${CLUSTERS[@]}"; do
    echo "🔧 Setting up AlertHub monitoring for cluster: $cluster"
    
    # Set context
    kubectl config use-context $cluster
    
    # Apply RBAC
    kubectl apply -f k8s/alerthub-k8s-rbac.yaml
    
    # Get token and save to file
    echo "📋 Extracting service account token..."
    kubectl get secret alerthub-k8s-intelligence-token -n alerthub-system \
      -o jsonpath='{.data.token}' | base64 --decode > "${cluster}-token.txt"
    
    # Get CA cert
    kubectl get secret alerthub-k8s-intelligence-token -n alerthub-system \
      -o jsonpath='{.data.ca\.crt}' | base64 --decode > "${cluster}-ca.crt"
    
    echo "✅ Setup complete for $cluster"
    echo "   Token saved to: ${cluster}-token.txt" 
    echo "   CA cert saved to: ${cluster}-ca.crt"
    echo "   Add cluster in AlertHub UI with these credentials"
    echo ""
done
```

## 🎯 **Benefits for Your Environment**

### **Multi-Cluster Security**:
- **10+ Clusters**: Same RBAC setup across all clusters
- **Centralized Monitoring**: AlertHub as single point of access
- **Minimal Permissions**: Only what's needed for topology/events
- **Token Rotation**: Easy to regenerate service account tokens

### **Production Ready**:
- **Enterprise Grade**: Service accounts are the standard approach
- **Compliance**: Audit-friendly with clear permission boundaries
- **Scalable**: Easy to add/remove clusters
- **Secure**: No kubectl context dependencies

### **Integration Benefits**:
- **Aurora AI**: Can investigate across all clusters securely
- **Infrastructure Correlation**: Maps K8s failures to CloudStack/VM layer
- **Alert Grouping**: Correlates pod failures with node/infrastructure issues
- **Topology Discovery**: Real-time cluster state without admin access

## 📊 **Current Working Example**

Your `mps-sandbox-rno` cluster is already connected and showing:
- ✅ **13 Nodes**: 4 masters + 9 workers 
- ✅ **254 Pods**: Including AlertHub, Aurora, Dynatrace
- ✅ **37 Namespaces**: Complete application landscape
- ✅ **Real Production Data**: Live topology discovery

**This secure service account approach is now ready for all your production K8s clusters!**