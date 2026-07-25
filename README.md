# goatway

An educational Go reverse-proxy gateway that reproduces the implementation ideas from three ZOZO Tech Blog articles.
It is designed for local study and verification, not production deployment.

## Overview

goatway is a lightweight gateway that handles:

- Reverse proxy (HTTP upstream forwarding)
- Regex-based routing
- Weighted round-robin / canary traffic splitting
- API token authentication + IP range restrictions
- Automatic retry (intra-group and cross-group)
- Backoff with full jitter
- Timeouts (connect / read / idle)
- Vendor-neutral OpenTelemetry tracing and W3C Trace Context propagation
- Client-disconnect detection (460)
- Development request-time override
- Per-client concurrent-request throttling with primary/canary pod-aware thresholds

## Article Mapping

| Feature | Article | Code Location |
|---|---|---|
| Reverse proxy | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/proxy/handler.go` |
| Routing (regex match) | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/router/matcher.go` |
| Weighted round-robin / canary traffic splitting | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/router/matcher.go` + `config/routes.yml` (commented example) |
| API token authentication | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/router/auth.go` |
| IP range restrictions | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/router/iprange.go` |
| Retry basics (retry cases: `server_error`, `timeout`) | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/proxy/retry.go` |
| Backoff & jitter | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/proxy/retry.go` (`retryBackoffCap`, `fullJitter`) |
| Timeouts (connect / read / idle) | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/proxy/client.go` + `internal/config/defaults.go` |
| Gateway-issued trace correlation (implemented with OpenTelemetry here) | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/telemetry/` + `internal/gateway/handler.go` + `internal/proxy/` |
| 460 (client disconnect) | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/proxy/handler.go` (`failed` method) |
| Development request-time override | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/router/requesttime.go` |
| Weighted round-robin scheduler internals | [Availability](https://techblog.zozo.com/entry/zozotown-api-gateway-availability) | `internal/scheduler/scheduler.go` |
| Cross-group retry (`retry_to_target_group_id`) | [Availability](https://techblog.zozo.com/entry/zozotown-api-gateway-availability) | `internal/proxy/retry.go` + `internal/targetgroup/group.go` |
| Timeout precedence | [Availability](https://techblog.zozo.com/entry/zozotown-api-gateway-availability) | `internal/config/defaults.go` + `internal/targetgroup/group.go` |
| Throttling (`IsOverLimit` formula) | [Throttling](https://techblog.zozo.com/entry/zozotown-api-gateway-throttling) | `internal/throttle/limiter.go` |
| Concurrent request limiting | [Throttling](https://techblog.zozo.com/entry/zozotown-api-gateway-throttling) | `internal/throttle/limiter.go` |
| Degradation (fetch-failure fallback) | [Throttling](https://techblog.zozo.com/entry/zozotown-api-gateway-throttling) | `internal/throttle/poller.go` (`FallbackThreshold`) |
| FileFetcher (replaces K8s / Istio) | [Throttling](https://techblog.zozo.com/entry/zozotown-api-gateway-throttling) | `internal/throttle/fetcher.go` |

The Intro article describes a gateway-issued trace ID and Datadog APM as separate operational choices. It does not prescribe OpenTelemetry, W3C Trace Context, or the header policies below. This repository adapts the correlation requirement with vendor-neutral OpenTelemetry APIs and an optional OTLP Collector.

## Getting Started

### 1. Start the example backends

The shipped configuration references ports `18081`, `18082`, and `18083`. Start all three for repeatable routing and retry demonstrations:

```powershell
# PowerShell
@"
package main
import ("fmt"; "net/http")
func main() {
    handler := func(port string) http.HandlerFunc {
        return func(w http.ResponseWriter, r *http.Request) {
            fmt.Fprintf(w, "hello from %s path=%s\n", port, r.URL.Path)
        }
    }
    go http.ListenAndServe(":18081", handler("18081"))
    go http.ListenAndServe(":18082", handler("18082"))
    http.ListenAndServe(":18083", handler("18083"))
}
"@ | Set-Content -Path "$env:TEMP\mock.go" -Encoding UTF8
go run "$env:TEMP\mock.go"
```

### 2. Run goatway

```powershell
go run ./cmd/goatway
```

By default it listens on `:8080` and loads configuration from `./config`.
You can override with environment variables:

```powershell
$env:GOATWAY_CONFIG_DIR = "./config"
$env:GOATWAY_LISTEN_ADDR = ":8080"
$env:GOATWAY_ENV = "dev"          # text logs when dev, JSON otherwise
```

Tracing works without an exporter. To export spans, point goatway at an OTLP/gRPC endpoint, normally an OpenTelemetry Collector:

```powershell
$env:OTEL_EXPORTER_OTLP_TRACES_ENDPOINT = "http://127.0.0.1:4317"
$env:OTEL_EXPORTER_OTLP_TRACES_PROTOCOL = "grpc"
go run ./cmd/goatway
```

`OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_EXPORTER_OTLP_PROTOCOL` are supported as fallbacks. Only the `grpc` protocol is accepted. With no endpoint, goatway still creates server, transfer, and client spans for trace correlation but exports nothing. The Collector configuration selects the backend, such as Tempo, Jaeger, or Datadog; goatway contains no vendor SDK dependency.

### 3. Stop

Press `Ctrl+C` for graceful shutdown. Goatway first gives HTTP requests up to 10 seconds to drain, then stops the deployment poller, then gives telemetry up to 10 seconds to flush and shut down.

## curl Verification Examples

### 200 with token authentication

```powershell
curl -i http://127.0.0.1:8080/sample/hello -H "Goatway-API-Token: abcde12345"
```

### 401 without token

```powershell
curl -i http://127.0.0.1:8080/sample/hello
```

### 403 with invalid token

```powershell
curl -i http://127.0.0.1:8080/sample/hello -H "Goatway-API-Token: invalid"
```

### 404 on unknown path

```powershell
curl -i http://127.0.0.1:8080/notfound -H "Goatway-API-Token: abcde12345"
```

### Trace ID propagation

```powershell
curl -i http://127.0.0.1:8080/sample/hello `
  -H "Goatway-API-Token: abcde12345" `
  -H "traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" `
  -H "tracestate: vendor=example"
```

The response `Goatway-Trace-ID` is `4bf92f3577b34da6a3ce929d0e0e4736`, the active OpenTelemetry TraceID. The upstream receives the same `Goatway-Trace-ID` plus a child `traceparent`; logs use that same identity. If the incoming `traceparent` is missing or invalid, the server span creates a new TraceID. A client-supplied `Goatway-Trace-ID` is never trusted.

The gateway installs its authoritative response trace header before routing, authentication, throttling, or proxying, so local 4xx/429/5xx responses also carry it. A backend cannot replace it.

## Header Trust Policy

Header handling is directional rather than a shared allowlist:

| Header | Incoming request | Upstream request / downstream response |
|---|---|---|
| `Goatway-API-Token` | Used for route authentication | Stripped before proxying |
| `Authorization`, `Cookie` | Not used by the gateway | Stripped before proxying |
| `Goatway-Request-Time` | Used only for the development override | Stripped before proxying |
| `Goatway-Trace-ID` | Client value ignored | Set from the active OTel TraceID upstream and on every gateway response; backend value stripped |
| `traceparent`, `tracestate` | Extracted by the server TraceContext propagator | Raw values removed, then the current client span context is injected upstream; backend response values stripped |
| `baggage` | Not propagated | Stripped in both directions |
| `Forwarded`, `X-Forwarded-*` | Treated as untrusted | Removed, then `X-Forwarded-For`, `X-Forwarded-Host`, and `X-Forwarded-Proto` are rebuilt from the direct connection for each attempt |
| `Set-Cookie` | Not applicable | Stripped from upstream responses |

Other hop-by-hop headers are removed in both directions. Trusted-proxy interpretation is intentionally not implemented.

### 429 (throttling) demo

Lower the value in `config/max_concurrent_requests.yml` and fire overlapping requests against a deliberately slow backend. Fast responses may complete before concurrency overlaps, so the deterministic version of this scenario lives in `internal/e2e/throttle_test.go`.

```powershell
# Example: set SampleClient: 1, then fire 2 requests at once
1..2 | ForEach-Object { Start-Job { curl -s -o NUL http://127.0.0.1:8080/sample/hello -H "Goatway-API-Token: abcde12345" } }
```

### Development request-time override

When `GOATWAY_ENV=dev`, you can override the request timestamp with `Goatway-Request-Time`:

```powershell
$env:GOATWAY_ENV = "dev"
go run ./cmd/goatway
curl -i http://127.0.0.1:8080/sample/hello -H "Goatway-API-Token: abcde12345" -H "Goatway-Request-Time: 2026-01-01T00:00:00Z"
```

## Configuration Reference

Configuration is loaded from six files under `config/`.

### target_groups.yml

Defines upstream target groups.

| Field | Type | Description |
|---|---|---|
| `targets` | array | `host`, `port`, `weight`, `retry_to`, `connect_timeout`, `read_timeout`, `idle_conn_timeout` |
| `scheme` | string | Upstream URL scheme: `"http"` or `"https"` (default: `"http"`). Target-level overrides group-level |
| `max_try_count` | int | Maximum attempts (0 means number of targets) |
| `retry_cases` | array | `"server_error"`, `"timeout"` |
| `retry_non_idempotent` | bool | Retry POST/PATCH as well |
| `retry_base_interval` | int | Milliseconds. Base backoff interval |
| `retry_max_interval` | int | Milliseconds. Backoff cap |
| `retry_to_target_group_id` | string | Cross-group retry destination group ID |
| `connect_timeout` | int | Milliseconds |
| `read_timeout` | int | Milliseconds |
| `idle_conn_timeout` | int | Milliseconds |
| `max_idle_conns_per_host` | int | Max idle connections per host |

### routes.yml

Routing rules from incoming paths to target groups.

| Field | Type | Description |
|---|---|---|
| `from.path` | string | Regex (e.g. `^/sample/(.+)$`) |
| `from.clients` | array | Allowed client types |
| `from.ip_range_groups` | array | Allowed IP range group names |
| `to.destinations` | array | `target_group`, `path`, `weight` |

### api_client_tokens.yml

Valid tokens per client type.

```yaml
SampleClient:
  - abcde12345
```

### ip_range_groups.yml

IP ranges in CIDR notation.

```yaml
SampleIPRange:
  - 127.0.0.1/32
  - ::1/128
```

### max_concurrent_requests.yml

Per-client-type maximum concurrent requests.

```yaml
SampleClient: 100
```

### deployment.yml

Deployment state read by `FileFetcher`. Replaces Kubernetes and Istio with a local file.

| Field | Type | Description |
|---|---|---|
| `primary_pods` | int | Number of primary pods |
| `canary_pods` | int | Number of canary pods |
| `primary_weight` | int | Primary traffic weight |
| `canary_weight` | int | Canary traffic weight |

## Out of Scope

The following are mentioned in the articles but omitted or replaced in this repository:

- **Member authentication**: Mentioned in the articles but not implemented in this codebase
- **AWS Secrets Manager / Athena / Datadog SDK / Sentry / PagerDuty / EKS**: Operational infrastructure integrations. A separately configured OTLP Collector may still export traces to Datadog or another backend
- **TLS termination and production hardening**: The example server is plain HTTP and is not a production edge gateway
- **Trusted proxy support**: IP restrictions use `RemoteAddr` directly. `Forwarded` and `X-Forwarded-For` are intentionally ignored, so deploy behind a proxy only after adding an explicit trusted-proxy model
- **Real Kubernetes / Istio clients**: Deployment state is read from a local YAML file instead
- **Prism / nginx mocks**: Mock tools used in the articles. Replaced here with `httptest` and local files
- **Health checks and circuit breakers**: Discussed as future availability work in the article, but not part of this reproduction
- **Operational alerts and `Retry-After` guidance**: The simplified gateway returns HTTP 429 without alerting integrations or a computed retry window
- **Streaming and body limits**: Requests and upstream responses are fully buffered in memory. Add explicit size limits or streaming before production use
- **Production-grade secrets and authentication**: The example token is public test data. Real deployments need managed secrets, TLS, stronger authentication, and token rotation

## Tests & CI

### Local run

```powershell
go run mvdan.cc/gofumpt@v0.10.0 -l .
go vet ./...
go build ./...
go test -shuffle=on -count=1 ./...
```

### Race detection

```powershell
go test -race -shuffle=on -count=1 ./...
```

On Windows, `-race` requires a GCC-compatible compiler and may not be available locally. GitHub Actions pins Go `1.26.5` and gofumpt `v0.10.0`, then runs formatting, vet, build, and race-enabled tests on Ubuntu.

## Directory Structure

```
.
├── cmd/goatway/                 # Entrypoint and owned server lifecycle
├── config/                      # Example configuration files
│   ├── target_groups.yml
│   ├── routes.yml
│   ├── api_client_tokens.yml
│   ├── ip_range_groups.yml
│   ├── max_concurrent_requests.yml
│   └── deployment.yml
├── internal/
│   ├── config/                  # Configuration loading & validation
│   ├── gateway/                 # Top-level HTTP handler
│   ├── logging/                 # slog wrapper
│   ├── proxy/                   # Upstream forwarding & retry
│   ├── router/                  # Routing, auth, IP restrictions
│   ├── scheduler/               # Weighted round-robin
│   ├── targetgroup/             # Target groups & registry
│   ├── telemetry/               # OTel provider, TraceContext, and optional OTLP exporter
│   └── throttle/                # Throttling & deployment-state fetching
├── go.mod
└── README.md
```
