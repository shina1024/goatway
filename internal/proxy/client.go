package proxy

import (
	"net"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"goatway/internal/targetgroup"
)

type clientKey struct {
	connectTimeout      time.Duration
	readTimeout         time.Duration
	idleConnTimeout     time.Duration
	maxIdleConnsPerHost int
}

type clientCache struct {
	mu        sync.Mutex
	clients   map[clientKey]clientCacheEntry
	telemetry clientTelemetry
}

type clientCacheEntry struct {
	client        *http.Client
	baseTransport *http.Transport
}

type clientTelemetry struct {
	provider   trace.TracerProvider
	propagator propagation.TextMapPropagator
}

func newClientCache() clientCache {
	return clientCache{
		clients: make(map[clientKey]clientCacheEntry),
		telemetry: clientTelemetry{
			provider:   otel.GetTracerProvider(),
			propagator: otel.GetTextMapPropagator(),
		},
	}
}

// WithTelemetry sets the dependencies used to instrument outbound client transports.
func WithTelemetry(provider trace.TracerProvider, propagator propagation.TextMapPropagator) Option {
	return func(handler *Handler) {
		if provider != nil {
			handler.clients.telemetry.provider = provider
		}
		if propagator != nil {
			handler.clients.telemetry.propagator = propagator
		}
	}
}

func (cache *clientCache) get(target targetgroup.Target, maxIdleConnsPerHost int) *http.Client {
	key := clientKey{
		connectTimeout:      target.ConnectTimeout(),
		readTimeout:         target.ReadTimeout(),
		idleConnTimeout:     target.IdleConnTimeout(),
		maxIdleConnsPerHost: maxIdleConnsPerHost,
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if entry, exists := cache.clients[key]; exists {
		return entry.client
	}
	baseTransport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: key.connectTimeout}).DialContext,
		IdleConnTimeout:     key.idleConnTimeout,
		MaxIdleConnsPerHost: key.maxIdleConnsPerHost,
	}
	client := &http.Client{
		Timeout: key.readTimeout,
		Transport: otelhttp.NewTransport(
			baseTransport,
			otelhttp.WithTracerProvider(cache.telemetry.provider),
			otelhttp.WithPropagators(cache.telemetry.propagator),
		),
	}
	cache.clients[key] = clientCacheEntry{client: client, baseTransport: baseTransport}
	return client
}
