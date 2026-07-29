# goatway

An educational Go reverse-proxy gateway that reproduces the implementation ideas from three ZOZO Tech Blog articles.
It is designed for local study and verification, not production deployment.

## Overview

goatway is a lightweight gateway that handles:

- Reverse proxy (HTTP upstream forwarding)
- Regex-based routing and path rewriting
- Weighted round-robin / canary traffic splitting
- API token authentication + IP range restrictions
- Automatic retry (intra-group and cross-group)
- Backoff with full jitter
- Timeouts (connect / read / idle)
- Gateway-issued trace correlation, implemented with OpenTelemetry in goatway
- Client-disconnect detection (460)
- Development request-time override
- Per-client concurrent-request throttling with primary/canary pod-aware thresholds

## Article Mapping

| Feature | Article | Code Location |
|---|---|---|
| Reverse proxy | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/proxy/handler.go` |
| Routing (regex match and path rewrite) | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/router/matcher.go` |
| Weighted round-robin / canary traffic splitting | [Availability](https://techblog.zozo.com/entry/zozotown-api-gateway-availability) | `internal/router/matcher.go` + `config/routes.yml` (commented example) |
| API token authentication | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/router/auth.go` |
| IP range restrictions | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/router/iprange.go` |
| Retry basics (retry cases: `server_error`, `timeout`) | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/proxy/retry.go` |
| Backoff & jitter | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/proxy/retry_schedule.go` (`retryBackoffCap`, `fullJitter`) |
| Timeouts (connect / read / idle) | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/proxy/client.go` + `internal/config/defaults.go` |
| Gateway-issued trace ID | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/gateway/handler.go` + `internal/proxy/handler.go` |
| 460 (client disconnect) | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/proxy/handler.go` (`failed` method) |
| Development request-time override | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/router/requesttime.go` |
| Weighted round-robin scheduler internals | [Availability](https://techblog.zozo.com/entry/zozotown-api-gateway-availability) | `internal/scheduler/scheduler.go` |
| Cross-group retry (`retry_to_target_group_id`) | [Availability](https://techblog.zozo.com/entry/zozotown-api-gateway-availability) | `internal/proxy/retry.go` + `internal/targetgroup/group.go` |
| Timeout precedence | [Availability](https://techblog.zozo.com/entry/zozotown-api-gateway-availability) | `internal/config/defaults.go` + `internal/targetgroup/group.go` |
| Throttling (`IsOverLimit` formula) | [Throttling](https://techblog.zozo.com/entry/zozotown-api-gateway-throttling) | `internal/throttle/limiter.go` |
| Concurrent request limiting | [Throttling](https://techblog.zozo.com/entry/zozotown-api-gateway-throttling) | `internal/throttle/limiter.go` |
| Degradation (fetch-failure fallback) | [Throttling](https://techblog.zozo.com/entry/zozotown-api-gateway-throttling) | `internal/throttle/poller.go` (`FallbackThreshold`) |

## Article Fidelity and Local Adaptations

The table above lists behavior described by the ZOZO articles. Goatway follows those application-level designs where they can be reproduced locally, while replacing ZOZOTOWN-specific infrastructure and vendor integrations:

| Area | ZOZO articles | Goatway adaptation |
|---|---|---|
| Trace correlation | The gateway issues a trace ID and adds it to the upstream request. The Intro article also describes Datadog APM issuing its own trace ID | One OpenTelemetry TraceID is used for the gateway response, logs, and upstream request. W3C `traceparent`/`tracestate` and optional OTLP/gRPC export are goatway additions |
| Header names | The articles describe custom headers for the API token, trace ID, and development request-time override without defining the names used by this repository | `Goatway-API-Token`, `Goatway-Trace-ID`, and `Goatway-Request-Time` are local names |
| Request forwarding | The articles do not define Cookie, Baggage, `Forwarded`, or `X-Forwarded-*` policies | Goatway applies the explicit forwarding rules documented below |
| Secrets and platform services | AWS Secrets Manager, Kubernetes, Istio, and operational SaaS products are used in the production system | Public YAML test data, local files, and an optional external OTel Collector replace those dependencies |
| Deployment state | Throttling uses Kubernetes/Istio deployment and traffic state | `FileFetcher` reads `deployment.yml` |
| Mocks and verification | Prism and nginx are used in the described development environment | `httptest` and simple local backends are used |

OpenTelemetry, W3C Trace Context, OTLP, and the forwarding rules are not claims about the source articles. They are vendor-neutral or security-focused choices made by goatway while preserving the article's gateway behavior.

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

API client tokens can be supplied outside the config directory. Inline YAML takes precedence over a file, and either source replaces `config/api_client_tokens.yml`:

```powershell
$env:GOATWAY_API_CLIENT_TOKENS_FILE = "C:\secrets\api_client_tokens.yml"
# Or: $env:GOATWAY_API_CLIENT_TOKENS_YAML = "SampleClient:`n  - secret-token"
```

As a goatway-specific observability adaptation, tracing works without an exporter. To export spans, point goatway at an OTLP/gRPC endpoint, normally an OpenTelemetry Collector:

```powershell
$env:OTEL_EXPORTER_OTLP_TRACES_ENDPOINT = "http://127.0.0.1:4317"
$env:OTEL_EXPORTER_OTLP_TRACES_PROTOCOL = "grpc"
go run ./cmd/goatway
```

RED metrics use `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` and `OTEL_EXPORTER_OTLP_METRICS_PROTOCOL`. `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_EXPORTER_OTLP_PROTOCOL` are supported as fallbacks for both signals. Only the `grpc` protocol is accepted. With no endpoint, goatway still creates server, transfer, and client spans for trace correlation but exports nothing. The Collector configuration selects the backend, such as Tempo, Jaeger, or Datadog; goatway contains no vendor SDK dependency.

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

Gateway-generated errors use an `application/json` body with `status`, `code`, `message`, and `trace_id` fields while preserving the HTTP status code. Upstream response bodies are forwarded unchanged.

### Health and readiness

```powershell
curl -i http://127.0.0.1:8080/healthz
curl -i http://127.0.0.1:8080/readyz
```

Both endpoints return HTTP 200 without exposing configuration or dependency details.

### Trace ID propagation (goatway OpenTelemetry adaptation)

```powershell
curl -i http://127.0.0.1:8080/sample/hello `
  -H "Goatway-API-Token: abcde12345" `
  -H "traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" `
  -H "tracestate: vendor=example"
```

The response `Goatway-Trace-ID` is `4bf92f3577b34da6a3ce929d0e0e4736`, the active OpenTelemetry TraceID. The upstream receives the same `Goatway-Trace-ID` plus a child `traceparent`; logs use that same identity. If the incoming `traceparent` is missing or invalid, the server span creates a new TraceID. A client-supplied `Goatway-Trace-ID` is never trusted.

The gateway installs its authoritative response trace header before routing, authentication, throttling, or proxying, so local 4xx/429/5xx responses also carry it. A backend cannot replace it.

## Goatway Request Forwarding Rules

The ZOZO articles do not prescribe these rules. Goatway handles request and response headers directionally rather than using one shared allowlist:

| Header | Incoming request handling | Forwarded upstream | Gateway response handling |
|---|---|---|---|
| `Goatway-API-Token` | Used for route authentication | Stripped | No special response rule |
| `Authorization`, `Cookie` | Not used by the gateway | Stripped | No special response rule |
| `Goatway-Request-Time` | Used only for the development override | Stripped | No special response rule |
| `Goatway-Trace-ID` | Client value ignored | Set from the active OTel TraceID | Gateway value is authoritative; backend value is stripped |
| `traceparent`, `tracestate` | Extracted by the server TraceContext propagator | Raw values are removed, then the current client span context is injected | Backend values are stripped |
| `baggage` | Not propagated | Stripped | Stripped |
| `Forwarded`, `X-Forwarded-*` | `Forwarded` is ignored. `X-Forwarded-For` affects IP restrictions only when the direct peer matches `gateway.yml` `trusted_proxies` | Removed, then `X-Forwarded-For`, `X-Forwarded-Host`, and `X-Forwarded-Proto` are rebuilt from the direct connection for each attempt | Not special-cased; backend response values pass through as ordinary end-to-end headers |
| `Set-Cookie` | Not applicable | Not applicable | Stripped from upstream responses |

Hop-by-hop headers are removed in both directions. With no configured trusted proxies, IP restrictions use `RemoteAddr` and ignore incoming forwarding headers. For a trusted direct peer, goatway evaluates every `X-Forwarded-For` header line from right to left and selects the first untrusted address. Forwarding-header rebuilding applies to upstream requests only; response headers are never used as client identity by goatway.

### 429 (throttling) demo

Lower the value in `config/max_concurrent_requests.yml` and fire overlapping requests against a deliberately slow backend. Fast responses may complete before concurrency overlaps, so the deterministic version of this scenario lives in `internal/e2e/throttle_test.go`.

```powershell
# Example: set SampleClient: 1, then fire 2 requests at once
1..2 | ForEach-Object { Start-Job { curl -s -o NUL http://127.0.0.1:8080/sample/hello -H "Goatway-API-Token: abcde12345" } }
```

### Development request-time override

When `GOATWAY_ENV=dev`, you can override the request timestamp with the goatway-specific `Goatway-Request-Time` header:

```powershell
$env:GOATWAY_ENV = "dev"
go run ./cmd/goatway
curl -i http://127.0.0.1:8080/sample/hello -H "Goatway-API-Token: abcde12345" -H "Goatway-Request-Time: 2026-01-01T00:00:00Z"
```

## Configuration Reference

Configuration is loaded from seven files under `config/`. `gateway.yml` is optional for backward compatibility; when present, it must declare `schema_version: 1`.

### gateway.yml

Controls gateway-level hardening settings.

| Field | Type | Description |
|---|---|---|
| `schema_version` | int | Required when the file exists; currently `1` |
| `proxy.max_response_body_size_bytes` | int | Maximum buffered upstream response body; default 10 MiB |
| `throttle.fail_policy` | string | `fail_open` (default) or `fail_closed` when deployment-state fetching degrades |
| `circuit_breaker.enabled` | bool | Enables one breaker per target group; default `false` |
| `circuit_breaker.failure_threshold` | int | Consecutive failures before opening; default `5` |
| `circuit_breaker.open_interval_ms` | int | Open interval before half-open probes; default `30000` |
| `circuit_breaker.half_open_max_requests` | int | Maximum probes admitted per half-open window; default `1` |
| `trusted_proxies` | array | Trusted proxy CIDRs allowed to supply client IPs through `X-Forwarded-For`; default empty |

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

Valid tokens per client type. `GOATWAY_API_CLIENT_TOKENS_FILE` or `GOATWAY_API_CLIENT_TOKENS_YAML` can replace this file at startup; inline YAML has highest precedence.

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
- **General proxy trust policy**: Configured CIDRs support trusted `X-Forwarded-For` interpretation for IP restrictions. Broader edge-proxy policy and the standardized `Forwarded` header remain out of scope
- **Real Kubernetes / Istio clients**: Deployment state is read from a local YAML file instead
- **Prism / nginx mocks**: Mock tools used in the articles. Replaced here with `httptest` and local files
- **Active health checking and availability automation**: `/healthz`, `/readyz`, and passive per-target-group circuit breakers are implemented; active upstream probes, alerting, and orchestration integration remain out of scope
- **Operational alerts and `Retry-After` guidance**: The simplified gateway returns HTTP 429 without alerting integrations or a computed retry window
- **Streaming**: Requests and upstream responses are buffered with size limits; streaming remains out of scope
- **Managed secrets and production authentication**: Tokens can be loaded from an external file or environment YAML, but managed secret stores, TLS, stronger authentication, and token rotation remain out of scope

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
│   ├── gateway.yml
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
