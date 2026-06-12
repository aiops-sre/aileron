# Quick Deployment Guide - MAS Auth Fix

## Changes Made

1. ✅ **LoginPage.tsx** - Auto-login with MAS
2. ✅ **OAuthCallbackPage.tsx** - Optimized for direct token handling
3. ✅ **authStore.ts** - Enhanced logout with session flags
4. ✅ **nginx.conf** - Fixed routing to send MAS auth to backend

## Deploy Steps

### Step 1: Build Frontend

```bash
# From sre-command-center directory
npm run build
```

### Step 2: Build Docker Image

```bash
docker build -t alerthub-frontend-sre-command-center:latest .
```

### Step 3: Tag and Push (if using registry)

```bash
# Tag for your registry
docker tag alerthub-frontend-sre-command-center:latest your-registry/alerthub-frontend-sre-command-center:latest

# Push to registry
docker push your-registry/alerthub-frontend-sre-command-center:latest
```

### Step 4: Update Kubernetes Deployment

```bash
# Restart the frontend pods to pick up new nginx.conf
kubectl rollout restart deployment/alerthub-frontend-v2 -n sre-hub-alerthub

# Or if using specific image
kubectl set image deployment/alerthub-frontend-v2 \
  alerthub-frontend-v2=your-registry/alerthub-frontend-sre-command-center:latest \
  -n sre-hub-alerthub
```

### Step 5: Verify Deployment

```bash
# Check pod status
kubectl get pods -n sre-hub-alerthub -l app=alerthub-v2,component=ui

# Check pod logs for any errors
kubectl logs -f deployment/alerthub-frontend-v2 -n sre-hub-alerthub

# Should see nginx start message
```

## Test After Deployment

1. **Clear browser cache** (important!)
2. **Open DevTools Console** (F12)
3. **Access the app**
4. **Check logs:**
   ```
   🔄 Auto-redirecting to MAS authentication...
   (redirect happens)
   🔍 OAuth Callback Page Loaded
   ✅ Direct token received in URL parameters
   ```

5. **Verify speed:**
   - Should complete in <1 second
   - No visible intermediate pages

## Rollback Plan

If there are issues:

```bash
# Rollback to previous deployment
kubectl rollout undo deployment/alerthub-frontend-v2 -n sre-hub-alerthub

# Check rollback status
kubectl rollout status deployment/alerthub-frontend-v2 -n sre-hub-alerthub
```

## Key Files Changed

1. [`src/pages/LoginPage.tsx`](src/pages/LoginPage.tsx:54) - Auto-login logic
2. [`src/pages/OAuthCallbackPage.tsx`](src/pages/OAuthCallbackPage.tsx:27) - Direct token support
3. [`src/stores/authStore.ts`](src/stores/authStore.ts:31) - Logout flag
4. [`nginx.conf`](nginx.conf:19) - MAS auth routing to backend

## Expected Performance

- **Before:** 2-5 seconds (OAuth code exchange)
- **After:** <1 second (direct token from backend)

## Monitoring

After deployment, monitor:

```bash
# Check nginx access logs
kubectl logs -f deployment/alerthub-frontend-v2 -n sre-hub-alerthub | grep "/api/v1/auth/mas"

# Should see requests being proxied to backend
```

## Backend Coordination

Share [`MAS_INGRESS_AUTH_OPTIMIZATION.md`](MAS_INGRESS_AUTH_OPTIMIZATION.md:1) with backend team so they can implement the fast token return method.

The frontend is now ready to support instant authentication once backend implements the optimized flow.
