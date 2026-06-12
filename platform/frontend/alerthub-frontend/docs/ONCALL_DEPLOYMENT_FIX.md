# Fix On-Call Page - Deployment Instructions

## 🎯 Goal: Get Real PagerDuty Data (No More CORS Errors!)

The On-Call page code is complete. To activate it, deploy the updated [`nginx.conf`](../nginx.conf) which proxies PagerDuty API calls with MAS authentication.

---

## 🚀 Deploy in 3 Steps:

### Step 1: Build Docker Image with Updated nginx.conf
```bash
cd "/Users/vishwakumarpatha/vishwa personnel/alerthub-enterprise/sre-command-center"

docker build -t ghcr.io/aileron-platform/sre-command-center:latest .
```

### Step 2: Push to Registry
```bash
docker push ghcr.io/aileron-platform/sre-command-center:latest
```

### Step 3: Restart Deployment
```bash
export KUBECONFIG=~/.kube/config:$(ls ~/.kube/configs/* | tr '\n' ':')
kubectl config use-context mps-sandbox-rno

kubectl rollout restart deployment/alerthub-v2 -n sre-hub-alerthub

# Wait for it to complete (takes 1-2 minutes)
kubectl rollout status deployment/alerthub-v2 -n sre-hub-alerthub --timeout=5m
```

### Step 4: Access & Verify
Open browser to:
```
https://alerthub-v2.k.example.com
```

Navigate to: **On-Call Schedule** page

**✅ Real PagerDuty data will now load!** No more CORS errors!

---

## 🔍 What Changed:

### Updated Files:
1. **[`nginx.conf`](../nginx.conf:47)** - Added `/pagerduty/*` proxy location
2. **[`src/services/PagerDutyService.ts`](../src/services/PagerDutyService.ts:20)** - Uses `/pagerduty` path
3. **[`src/pages/OnCallSchedule.tsx`](../src/pages/OnCallSchedule.tsx:663)** - Fixed crashes (null safety)

### How It Works:
```
Browser calls:     /pagerduty/oncall/current
                           ↓
Nginx rewrites to: /api/v1/oncall/current
                           ↓
Nginx proxies to:  https://oncall-pd.k.example.com/api/v1/oncall/current
                           ↓ (with MAS headers)
oncall-pd returns: Real data ✅
```

---

## ✅ Verification Checklist:

After deployment, check:
- [ ] On-Call Schedule page loads without errors
- [ ] Current on-call personnel displays
- [ ] Upcoming shifts show correct data
- [ ] Statistics display (incidents, resolution time)
- [ ] No 404/CORS errors in browser console
- [ ] Data refreshes automatically

---

## 🐛 If Issues Occur:

### Check pod logs:
```bash
kubectl logs -n sre-hub-alerthub -l app=alerthub-v2 -c nginx --tail=50
```

### Check pod status:
```bash
kubectl get pods -n sre-hub-alerthub -l app=alerthub-v2
```

### Force pod restart:
```bash
kubectl delete pod -n sre-hub-alerthub -l app=alerthub-v2
```

---

## 📝 Note:

The Dockerfile automatically copies `nginx.conf` into the image, so rebuilding the image includes the PagerDuty proxy configuration.
