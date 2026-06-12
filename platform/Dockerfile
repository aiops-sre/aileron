FROM golang:1.24-alpine AS builder

# Accept corporate proxy as build args (passed by buildkit job)
ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG NO_PROXY=*.example.com,ghcr.io/aileron-platform,github.com/aileron-platform
ARG GOPROXY=https://proxy.golang.org,direct
ARG GONOSUMDB=*

ENV HTTP_PROXY=${HTTP_PROXY} \
    HTTPS_PROXY=${HTTPS_PROXY} \
    NO_PROXY=${NO_PROXY} \
    GOPROXY=${GOPROXY} \
    GONOSUMDB=${GONOSUMDB}

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o alerthub ./cmd/main.go

FROM alpine:3.19

RUN addgroup -S alerthub && adduser -S alerthub -G alerthub

WORKDIR /app

COPY --from=builder /build/alerthub /app/alerthub

RUN chown -R alerthub:alerthub /app

USER alerthub

EXPOSE 3000

ENTRYPOINT ["/app/alerthub"]
