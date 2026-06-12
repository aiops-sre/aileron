# PagerDuty Integration Setup Guide

## 🔴 CORS Error Fix - Getting Real Data

The CORS errors you're seeing are caused by **MAS (Miao Authentication Service) authentication** requirements. Here's how to fix it:

---

## ✅ Solution 1: Access via Kubernetes Ingress (RECOMMENDED)

### Instead of accessing:
```
http://localhost:3001
```

### Access your app via the Kubernetes ingress URL:
```
https://alerthub-v2.k.example.com
```

### Why this works:
- ✅ MAS auth is already configured for your domain
- ✅ Both apps share cookie domain (`.k.example.com`)
- ✅ MAS injects auth headers automatically
- ✅ No CORS issues (both on same domain)
- ✅ **Real data will load immediately**

### Steps:
1. Open your browser
2. Navigate to: `https://alerthub-v2.k.example.com`
3. Login with MAS if prompted
4. Navigate to the On-Call Schedule page
5. **Real PagerDuty data will now load!**

---

## 🛠️ Solution 2: Local Development with Real Data

If you need to develop on localhost:3001, run the on-call-pd backend locally:

### 1. Navigate to on-call-pd directory:
```bash
cd "/Users/vishwakumarpatha/vishwa personnel/on-call-pd"
```

### 2. Set up environment:
```bash
cp .env.example .env
```

### 3. Edit `.env` with your PagerDuty API key:
```env
PAGERDUTY_API_KEY=your_api_key_here
PAGERDUTY_SCHEDULE_IDS=YOUR_SCHEDULE_IDS
PORT=3000
```

### 4. Run with Docker Compose (includes Redis & PostgreSQL):
```bash
docker-compose up -d
```

### 5. OR run locally with Go:
```bash
# Start dependencies
docker run -d --name postgres -p 5432:5432 \
  -e POSTGRES_USER=oncall \
  -e POSTGRES_PASSWORD=oncall_password \
  -e POSTGRES_DB=oncall_manager \
  postgres:15-alpine

docker run -d --name redis -p 6379:6379 redis:7-alpine

# Run backend
go run cmd/server/main.go
```

### 6. Verify backend is running:
```bash
curl http://localhost:3000/api/v1/health
```

### 7. Access frontend:
```
http://localhost:3001
```

Now the frontend will call `http://localhost:3000` (same origin, no CORS!)

---

## 📊 How It Works

### Architecture:

```
┌─────────────────────────────────────────────────────────────┐
│ Browser @ localhost:3001                                     │
│  ├─ Frontend (sre-command-center)                            │
│  └─ Calls: http://localhost:3000 ────┐                      │
└───────────────────────────────────────┼──────────────────────┘
                                        │
                                        ▼
┌─────────────────────────────────────────────────────────────┐
│ Backend @ localhost:3000 (on-call-pd)                       │
│  ├─ Handles auth with PagerDuty                              │
│  ├─ Returns real data                                        │
│  └─ No CORS issues (same localhost)                          │
└─────────────────────────────────────────────────────────────┘
```

### In Production:

```
┌─────────────────────────────────────────────────────────────┐
│ Browser @ https://alerthub-v2.k.example.com              │
│  ├─ Frontend (sre-command-center)                            │
│  │   Namespace: sre-hub-alerthub                             │
│  └─ Calls: https://oncall-pd.k.example.com ──┐           │
└───────────────────────────────────────────────────┼──────────┘
                                                    │
                         ┌──────────────────────────┘
                         │ ✅ Same domain (.k.example.com)
                         │ ✅ MAS auth cookies shared
                         │ ✅ No CORS issues
                         ▼
┌─────────────────────────────────────────────────────────────┐
│ on-call-pd @ https://oncall-pd.k.example.com             │
│  ├─ Backend service                                          │
│  │   Namespace: sre-hub-sandbox                              │
│  └─ Returns real PagerDuty data                              │
└─────────────────────────────────────────────────────────────┘
```

---

## 🚨 Common Issues

### Issue: "Preflight response is not successful. Status code: 302"
**Cause**: Accessing `http://localhost:3001` and calling production API
**Fix**: Use Solution 1 (access via https://alerthub-v2.k.example.com)

### Issue: "Failed to load resource: net::ERR_CONNECTION_REFUSED" 
**Cause**: on-call-pd not running on localhost:3000
**Fix**: Run on-call-pd backend locally (Solution 2)

---

## 🎯 Quick Summary

| Scenario | Frontend URL | Backend URL | Auth | Works? |
|----------|-------------|-------------|------|--------|
| **Production (BEST)** | `https://alerthub-v2.k.example.com` | `https://oncall-pd.k.example.com` | MAS | ✅ Yes |
| **Local Dev** | `http://localhost:3001` | `http://localhost:3000` | None needed | ✅ Yes |
| **❌ Won't Work** | `http://localhost:3001` | `https://oncall-pd.k.example.com` | MAS (302 redirect) | ❌ CORS Error |

---

## 📝 Next Steps

**For immediate real data access:**
1. Open browser
2. Navigate to: `https://alerthub-v2.k.example.com`
3. Go to "On-Call Schedule" page
4. Real data loads automatically!

**For local development:**
1. Run on-call-pd backend on port 3000
2. Access frontend at `http://localhost:3001`
3. Both communicate without CORS

---

## 🔧 Code Configuration

The [`PagerDutyService`](../src/services/PagerDutyService.ts:20) automatically detects the environment:

```typescript
BASE_URL: window.location.hostname === 'localhost' 
  ? 'http://localhost:3000'  // Local development
  : 'https://oncall-pd.k.example.com'  // Production
```

No code changes needed - it works automatically! 🎉
