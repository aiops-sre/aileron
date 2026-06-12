#!/bin/bash
# AlertHub Enterprise - Build and Push All Missing Images
# Run this script from the project root directory
# Usage: bash build_and_push.sh

set -e

REGISTRY="ghcr.io/aileron-platform/aileron-admins"
PROJECT_ROOT="$(cd "$(dirname "$0")" && pwd)"

echo "🚀 Building and pushing AlertHub Enterprise microservice images..."
echo "Registry: $REGISTRY"
echo ""

build_and_push() {
  local name="$1"
  local tag="$2"
  local context="$3"
  echo "📦 Building $name:$tag from $context..."
  docker buildx build --platform linux/amd64 \
    -t "$REGISTRY/$name:$tag" \
    --push \
    "$context"
  echo "✅ $name:$tag pushed successfully"
  echo ""
}

# ai-correlation-engine
build_and_push "ai-correlation-engine" "v2.1.1" \
  "$PROJECT_ROOT/services/ai/ai-correlation-engine"

# ai-services-manager
build_and_push "ai-services-manager" "v3.0.0" \
  "$PROJECT_ROOT/services/ai/ai-services-manager"

# alerthub-bert-service (Flask, port 8765)
build_and_push "alerthub-bert-service" "v3.0.0" \
  "$PROJECT_ROOT/services/ai/alerthub-bert-service"

# local-bert-service (Flask, port 8766)
build_and_push "local-bert-service" "v3.0.0" \
  "$PROJECT_ROOT/services/local-bert"

# autonomous-learning-engine (no tensorflow - removed from requirements)
build_and_push "autonomous-learning-engine" "v3.0.0" \
  "$PROJECT_ROOT/services/ai/autonomous-learning-engine"

# ai-investigation-engine (uses mock_main.py - self-contained)
build_and_push "ai-investigation-engine" "v3.0.1" \
  "$PROJECT_ROOT/services/ai/ai-investigation-engine"

# vector-embedding-service (TRANSFORMERS_OFFLINE=0 fixed)
build_and_push "vector-embedding-service" "v2.0.1" \
  "$PROJECT_ROOT/services/ai/vector-embedding"

echo "🎉 All images built and pushed successfully!"
echo ""
echo "Next steps - restore real images in K8s deployments (run these after all pushes complete):"
echo ""
echo "# Restore real images (removes placeholder servers):"
echo "  kubectl set image deployment/ai-correlation-engine correlation-engine=$REGISTRY/ai-correlation-engine:v2.1.1 -n aileron"
echo "  kubectl set image deployment/ai-services-manager ai-services-manager=$REGISTRY/ai-services-manager:v3.0.0 -n aileron"
echo "  kubectl set image deployment/alerthub-bert-service bert-service=$REGISTRY/alerthub-bert-service:v3.0.0 -n aileron"
echo "  kubectl set image deployment/autonomous-learning-engine learning-engine=$REGISTRY/autonomous-learning-engine:v3.0.0 -n aileron"
echo "  kubectl set image deployment/local-bert-service local-bert=$REGISTRY/local-bert-service:v3.0.0 -n aileron"
echo "  kubectl set image deployment/ai-investigation-engine investigation-engine=$REGISTRY/ai-investigation-engine:v3.0.1 -n aileron"
echo "  kubectl set image deployment/vector-embedding-service vector-embedding=$REGISTRY/vector-embedding-service:v2.0.1 -n aileron"
echo ""
echo "# Also remove the command/args overrides (placeholder servers used these):"
echo "  for svc in ai-correlation-engine ai-services-manager alerthub-bert-service autonomous-learning-engine local-bert-service ai-investigation-engine; do"
echo "    kubectl patch deployment \$svc -n aileron --type=json -p='[{\"op\":\"remove\",\"path\":\"/spec/template/spec/containers/0/command\"},{\"op\":\"remove\",\"path\":\"/spec/template/spec/containers/0/args\"}]' 2>/dev/null || true"
echo "  done"
echo ""
echo "# Watch rollout status:"
echo "  kubectl get pods -n aileron -w"
