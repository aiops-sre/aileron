FROM golang:1.24-alpine AS builder

ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG NO_PROXY
ARG GOPROXY=http://:80|https://proxy.golang.org|direct
ARG GONOSUMCHECK=*
ARG GOFLAGS=-mod=mod
ARG VERSION=dev
ARG CACHE_BUST=1

ENV HTTP_PROXY=${HTTP_PROXY} \
    HTTPS_PROXY=${HTTPS_PROXY} \
    NO_PROXY=${NO_PROXY} \
    GOPROXY=${GOPROXY} \
    GONOSUMDB=* \
    GONOSUMCHECK=${GONOSUMCHECK} \
    GOFLAGS=${GOFLAGS} \
    GOTOOLCHAIN=auto \
    CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

# Build context is repo root. OIE source lives under services/oie/.
WORKDIR /build

# Copy OIE module files first for layer caching.
COPY services/oie/go.mod services/oie/go.sum ./services/oie/

# Download dependencies using the workspace-resolved go.sum.
WORKDIR /build/services/oie
RUN go mod download

# Copy OIE source.
COPY services/oie/ .

RUN go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /oie \
    ./cmd/oie/

FROM alpine:3.19 AS runtime

RUN apk --no-cache add ca-certificates tzdata && \
    addgroup -S oie && adduser -S oie -G oie

# Trust Apple Corporate Root CAs — required for ONTAP REST API, IdMS JWKS endpoint,
# CloudStack API, and all other internal Apple services reached by OIE fetchers.
COPY services/oie/apple-corp-cas.crt /usr/local/share/ca-certificates/apple-corp.crt
RUN update-ca-certificates

COPY --from=builder /oie /usr/local/bin/oie

USER oie:oie

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/oie"]
