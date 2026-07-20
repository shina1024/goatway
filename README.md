# goatway

A Go reverse-proxy gateway that reproduces the three-part ZOZO Tech Blog series **"Building an API Gateway from Scratch"**.
This repository lets you run and verify every feature discussed in the articles with actual code.

## Overview

goatway is a lightweight gateway that handles:

- Reverse proxy (HTTP upstream forwarding)
- Regex-based routing
- Weighted round-robin / canary traffic splitting
- API token authentication + IP range restrictions
- Automatic retry (intra-group and cross-group)
- Backoff with full jitter
- Timeouts (connect / read / idle)
- Trace ID generation and propagation
- Client-disconnect detection (460)
- Development request-time override
- Throttling (per-pod concurrent connection limits)

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
| Trace ID generation & propagation | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/proxy/handler.go` + `internal/gateway/handler.go` |
| 460 (client disconnect) | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/proxy/handler.go` (`failed` method) |
| Development request-time override | [Intro](https://techblog.zozo.com/entry/zozotown-api-gateway-intro) | `internal/router/requesttime.go` |
| Weighted round-robin scheduler internals | [Availability](https://techblog.zozo.com/entry/zozotown-api-gateway-availability) | `internal/scheduler/scheduler.go` |
| Cross-group retry (`retry_to_target_group_id`) | [Availability](https://techblog.zozo.com/entry/zozotown-api-gateway-availability) | `internal/proxy/retry.go` + `internal/targetgroup/group.go` |
| Timeout precedence | [Availability](https://techblog.zozo.com/entry/zozotown-api-gateway-availability) | `internal/config/defaults.go` + `internal/targetgroup/group.go` |
| Throttling (`IsOverLimit` formula) | [Throttling](https://techblog.zozo.com/entry/zozotown-api-gateway-throttling) | `internal/throttle/limiter.go` |
| Concurrent request limiting | [Throttling](https://techblog.zozo.com/entry/zozotown-api-gateway-throttling) | `internal/throttle/limiter.go` |
| Degradation (fetch-failure fallback) | [Throttling](https://techblog.zozo.com/entry/zozotown-api-gateway-throttling) | `internal/throttle/poller.go` (`FallbackThreshold`) |
| FileFetcher (replaces K8s / Istio) | [Throttling](https://techblog.zozo.com/entry/zozotown-api-gateway-throttling) | `internal/throttle/fetcher.go` |

## Getting Started

### 1. Start a backend (optional)

To test locally you need an upstream backend. Here is a minimal Go mock:

```powershell
# PowerShell
@"
package main
import ("fmt"; "net/http"; "time")
func main() {
    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprintf(w, "hello from %s\n", r.URL.Path)
    })
    http.ListenAndServe(":18081", nil)
}
"@ | Set-Content -Path "$env:TEMP\mock.go" -Encoding UTF8
go run "$env:TEMP\mock.go"
```

Or with Python:

```powershell
python -m http.server 18081
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

### 3. Stop

Press `Ctrl+C` for graceful shutdown (10-second drain window).

## curl Verification Examples

### 200 with token authentication

```powershell
curl -i http://127.0.0.1:8080/sample/hello -H "X-Goatway-API-Token: abcde12345"
```

### 401 without token

```powershell
curl -i http://127.0.0.1:8080/sample/hello
```

### 403 with invalid token

```powershell
curl -i http://127.0.0.1:8080/sample/hello -H "X-Goatway-API-Token: invalid"
```

### 404 on unknown path

```powershell
curl -i http://127.0.0.1:8080/notfound -H "X-Goatway-API-Token: abcde12345"
```

### Trace ID propagation

```powershell
curl -i http://127.0.0.1:8080/sample/hello -H "X-Goatway-API-Token: abcde12345" -H "X-Goatway-Trace-ID: my-trace-123"
```

The response includes the `X-Goatway-Trace-ID` header, and the gateway logs carry the same ID.

### 429 (throttling) demo

Lower the value in `config/max_concurrent_requests.yml` and fire multiple concurrent requests:

```powershell
# Example: set SampleClient: 1, then fire 2 requests at once
1..2 | ForEach-Object { Start-Job { curl -s -o NUL http://127.0.0.1:8080/sample/hello -H "X-Goatway-API-Token: abcde12345" } }
```

### Development request-time override

When `GOATWAY_ENV=dev`, you can override the request timestamp with `X-Goatway-Request-Time`:

```powershell
$env:GOATWAY_ENV = "dev"
go run ./cmd/goatway
curl -i http://127.0.0.1:8080/sample/hello -H "X-Goatway-API-Token: abcde12345" -H "X-Goatway-Request-Time: 2026-01-01T00:00:00Z"
```

## Configuration Reference

Configuration is loaded from six files under `config/`.

### target_groups.yml

Defines upstream target groups.

| Field | Type | Description |
|---|---|---|
| `targets` | array | `host`, `port`, `weight`, `retry_to`, `connect_timeout`, `read_timeout`, `idle_conn_timeout` |
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
- **AWS Secrets Manager / Athena / Datadog / Sentry / PagerDuty / EKS**: Operational infrastructure integrations
- **Prism / nginx mocks**: Mock tools used in the articles. Replaced here with `httptest` and local files
- **Full in-memory request body buffering**: The current implementation buffers the entire request body in memory before forwarding upstream. This is a simplicity trade-off

## Tests & CI

### Local run

```powershell
go test ./...
```

### Race detection

```powershell
go test -race ./...
```

On Windows, `-race` requires a GCC-compatible compiler and may not be available locally. It is recommended to run `-race` on CI (Ubuntu).

## Directory Structure

```
.
├── cmd/goatway/main.go          # Entrypoint (wiring only)
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
│   └── throttle/                # Throttling & deployment-state fetching
├── go.mod
└── README.md
```
