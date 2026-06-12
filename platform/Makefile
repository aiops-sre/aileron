# AlertHub Enterprise - Makefile

.PHONY: help build test deploy clean docs

# Default target
help: ## Show this help message
	@echo "AlertHub Enterprise - Available Commands:"
	@echo ""
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo ""

# Development
dev-setup: ## Set up development environment
	@echo "Setting up AlertHub Enterprise development environment..."
	@./scripts/build/dev-setup.sh

build-all: ## Build all services
	@echo "Building all AlertHub services..."
	@./scripts/build/build-and-push.sh

build-go: ## Build Go microservices
	@echo "Building Go services..."
	@for service in services/alerthub-*-service; do \
		if [ -f "$$service/cmd/main.go" ]; then \
			echo "Building $$service..."; \
			cd $$service && go build -o bin/service cmd/main.go && cd ../..; \
		fi \
	done

build-frontend: ## Build React frontend
	@echo "Building frontend..."
	@cd services/alerthub-frontend && npm run build

# Testing
test-all: ## Run all tests
	@echo "Running all tests..."
	@./scripts/test/test-integration.sh

test-go: ## Test Go services
	@echo "Testing Go services..."
	@for service in services/alerthub-*-service; do \
		if [ -f "$$service/go.mod" ]; then \
			echo "Testing $$service..."; \
			cd $$service && go test ./... && cd ../..; \
		fi \
	done

test-frontend: ## Test React frontend
	@echo "Testing frontend..."
	@cd services/alerthub-frontend && npm test

# Deployment
deploy-local: ## Deploy locally with Docker Compose
	@echo "Deploying AlertHub locally..."
	@docker-compose -f docker/docker-compose.yml up -d

deploy-k8s: ## Deploy to Kubernetes
	@echo "Deploying to Kubernetes..."
	@./scripts/deploy/master-deployment.sh

deploy-dev: ## Deploy to development environment
	@echo "Deploying to development..."
	@kubectl apply -f infrastructure/ -n alerthub-dev

deploy-prod: ## Deploy to production environment
	@echo "Deploying to production..."
	@./scripts/deploy/master-deployment.sh --env=production

# Database
db-migrate: ## Run database migrations
	@echo "Running database migrations..."
	@./scripts/migration/apply-migrations.sh

db-setup: ## Set up databases
	@echo "Setting up databases..."
	@./scripts/deploy/setup-databases.sh

# Documentation
docs-build: ## Build documentation
	@echo "Building documentation..."
	@./scripts/build/build-docs.sh

docs-serve: ## Serve documentation locally
	@echo "Serving documentation on http://localhost:8080..."
	@cd docs && python3 -m http.server 8080

# Monitoring
monitor-setup: ## Set up monitoring stack
	@echo "Setting up monitoring..."
	@kubectl apply -f infrastructure/monitoring/

logs-follow: ## Follow all service logs
	@echo "Following service logs..."
	@kubectl logs -f -l app.kubernetes.io/part-of=alerthub

# Validation
validate: ## Validate project structure
	@echo "Validating project structure..."
	@./scripts/validation/validate-structure.sh

validate-k8s: ## Validate Kubernetes manifests
	@echo "Validating Kubernetes manifests..."
	@./scripts/validation/validate-k8s.sh

# Cleanup
clean: ## Clean build artifacts
	@echo "Cleaning build artifacts..."
	@find services/ -name "bin" -type d -exec rm -rf {} +
	@find services/ -name "dist" -type d -exec rm -rf {} +
	@find services/ -name "node_modules" -type d -exec rm -rf {} +

clean-docker: ## Clean Docker images and containers
	@echo "Cleaning Docker resources..."
	@docker system prune -f
	@docker image prune -f

# Security
security-scan: ## Run security scans
	@echo "Running security scans..."
	@./scripts/validation/security-tests.sh

# Performance
benchmark: ## Run performance benchmarks
	@echo "Running performance benchmarks..."
	@./scripts/validation/performance-tests.sh

# Service Management
services-status: ## Check status of all services
	@echo "Checking service status..."
	@kubectl get pods -l app.kubernetes.io/part-of=alerthub

services-restart: ## Restart all services
	@echo "Restarting services..."
	@kubectl rollout restart deployment -l app.kubernetes.io/part-of=alerthub

# Proto Generation
proto-gen: ## Generate protobuf code
	@echo "Generating protobuf code..."
	@./scripts/build/generate-proto.sh

# Configuration
config-validate: ## Validate all configurations
	@echo "Validating configurations..."
	@./scripts/validation/validate-configs.sh

config-sync: ## Sync configuration to cluster
	@echo "Syncing configuration..."
	@kubectl apply -f infrastructure/config/

# Version Management
version: ## Show version information
	@echo "AlertHub Enterprise Version Information:"
	@echo "Git Commit: $$(git rev-parse HEAD)"
	@echo "Git Branch: $$(git branch --show-current)"
	@echo "Build Date: $$(date)"

release: ## Create a new release
	@echo "Creating new release..."
	@./scripts/deploy/create-release.sh
