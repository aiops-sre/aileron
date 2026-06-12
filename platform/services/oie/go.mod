module github.com/aileron-platform/aileron/platform/services/oie

go 1.24

require (
	github.com/IBM/sarama v1.47.0
	github.com/gin-gonic/gin v1.9.1
	github.com/google/uuid v1.6.0
	github.com/lib/pq v1.10.9
	github.com/prometheus/client_golang v1.23.2
	github.com/redis/go-redis/v9 v9.0.5
	github.com/stretchr/testify v1.8.4
	go.opentelemetry.io/otel v1.39.0
	go.opentelemetry.io/otel/exporters/jaeger v1.17.0
	go.opentelemetry.io/otel/sdk v1.39.0
	go.opentelemetry.io/otel/trace v1.39.0
	k8s.io/api v0.28.3
	k8s.io/apimachinery v0.28.3
	k8s.io/client-go v0.28.3
)

replace github.com/IBM/sarama => github.com/Shopify/sarama v1.38.1
