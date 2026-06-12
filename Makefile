REGISTRY  ?= ghcr.io/aileron-platform
TAG       ?= $(shell git rev-parse --short HEAD)
COMPOSE   ?= docker compose

PLATFORM_IMAGE   := $(REGISTRY)/aileron-platform:$(TAG)
OIE_IMAGE        := $(REGISTRY)/aileron-oie:$(TAG)
AGENT_IMAGE      := $(REGISTRY)/aileron-agent:$(TAG)
COLLECTOR_IMAGE  := $(REGISTRY)/aileron-collector:$(TAG)

.PHONY: dev dev-deps test \
        build-platform build-oie build-agent build-collector build \
        push helm-install clean

## ─── Development ──────────────────────────────────────────────────────────────

# Start the full stack (build from source)
dev:
	$(COMPOSE) up --build

# Start only infrastructure dependencies (postgres, redis, kafka, neo4j, ollama)
dev-deps:
	$(COMPOSE) up postgres redis zookeeper kafka neo4j ollama

## ─── Tests ────────────────────────────────────────────────────────────────────

test:
	@echo ">>> Testing platform (Go 1.24)"
	cd platform && go test ./... -race -timeout 120s
	@echo ">>> Testing OIE (Go 1.24)"
	cd platform/services/oie && go test ./... -race -timeout 120s
	@echo ">>> Testing agent (Go 1.22)"
	cd agent && go test ./... -race -timeout 120s
	@echo ">>> Testing frontend"
	cd platform/frontend/alerthub-frontend && npm ci --prefer-offline && npm test -- --watchAll=false

## ─── Docker builds ────────────────────────────────────────────────────────────

build-platform:
	docker build \
		-t $(PLATFORM_IMAGE) \
		-t $(REGISTRY)/aileron-platform:latest \
		./platform

build-oie:
	docker build \
		-t $(OIE_IMAGE) \
		-t $(REGISTRY)/aileron-oie:latest \
		./platform/services/oie

build-agent:
	docker build \
		-f ./agent/Dockerfile.agent \
		-t $(AGENT_IMAGE) \
		-t $(REGISTRY)/aileron-agent:latest \
		./agent

build-collector:
	docker build \
		-f ./agent/Dockerfile.collector \
		-t $(COLLECTOR_IMAGE) \
		-t $(REGISTRY)/aileron-collector:latest \
		./agent

build: build-platform build-oie build-agent build-collector

## ─── Push ─────────────────────────────────────────────────────────────────────

push: build
	docker push $(PLATFORM_IMAGE)
	docker push $(REGISTRY)/aileron-platform:latest
	docker push $(OIE_IMAGE)
	docker push $(REGISTRY)/aileron-oie:latest
	docker push $(AGENT_IMAGE)
	docker push $(REGISTRY)/aileron-agent:latest
	docker push $(COLLECTOR_IMAGE)
	docker push $(REGISTRY)/aileron-collector:latest

## ─── Helm ─────────────────────────────────────────────────────────────────────

HELM_RELEASE  ?= aileron
HELM_NS       ?= aileron
HELM_CHART    ?= oci://ghcr.io/aileron-platform/charts/aileron

helm-install:
	helm upgrade --install $(HELM_RELEASE) $(HELM_CHART) \
		--namespace $(HELM_NS) \
		--create-namespace \
		--set image.tag=$(TAG)

## ─── Cleanup ──────────────────────────────────────────────────────────────────

clean:
	$(COMPOSE) down -v
