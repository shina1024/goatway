// Package proxy performs a single, retry-free HTTP transfer to a selected target.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"

	"goatway/internal/circuitbreaker"
	"goatway/internal/headers"
	"goatway/internal/router"
	"goatway/internal/targetgroup"
	"goatway/internal/telemetry"
)

const clientClosedStatus = 460

var (
	ErrNilTargetGroup   = errors.New("proxy: nil target group")
	ErrMissingRoutePath = errors.New("proxy: missing routed path")
)

const defaultMaxResponseBodySize = 10 << 20

// ErrClass identifies a transport outcome that a later retry policy may inspect.
type ErrClass uint8

const (
	ErrClassNone ErrClass = iota
	ErrClassTimeout
	ErrClassOther
)

// AttemptResult describes the single upstream attempt without making a retry decision.
type AttemptResult struct {
	StatusCode int
	ErrClass   ErrClass
}

// ForwardInput identifies one target and the route metadata used to build its request.
type ForwardInput struct {
	Target targetgroup.Target
	Group  *targetgroup.TargetGroup
	Match  router.Match
}

// BufferedAttempt combines one route selection with a replayable request body.
type BufferedAttempt struct {
	Input ForwardInput
	Body  BufferedBody
}

// Option configures a Handler.
type Option func(*Handler)

// WithLogger sets the logger used for client-disconnect warnings.
func WithLogger(logger *slog.Logger) Option {
	return func(handler *Handler) {
		if logger != nil {
			handler.logger = logger
		}
	}
}

// WithMaxResponseBodySize sets the maximum buffered upstream response size.
func WithMaxResponseBodySize(bytes int64) Option {
	return func(handler *Handler) {
		if bytes > 0 {
			handler.maxResponseBodySize = bytes
		}
	}
}

// WithRetrySleeper replaces the delay function used between retry attempts.
func WithRetrySleeper(sleeper func(time.Duration)) Option {
	return func(handler *Handler) {
		if sleeper != nil {
			handler.retryWaiter = func(_ context.Context, delay time.Duration) error {
				sleeper(delay)
				return nil
			}
		}
	}
}

func withRetryWaiter(waiter func(context.Context, time.Duration) error) Option {
	return func(handler *Handler) {
		if waiter != nil {
			handler.retryWaiter = waiter
		}
	}
}

// Handler owns reusable HTTP clients and performs one forwarding attempt at a time.
type Handler struct {
	clients             clientCache
	circuitBreakers     *circuitbreaker.Registry
	logger              *slog.Logger
	retryWaiter         func(context.Context, time.Duration) error
	maxResponseBodySize int64
	metrics             *telemetry.Metrics
}

// WithMetrics sets the RED metric instruments used by retry forwarding.
func WithMetrics(metrics *telemetry.Metrics) Option {
	return func(handler *Handler) {
		handler.metrics = metrics
	}
}

// WithCircuitBreakers sets the per-target-group circuit breaker registry.
func WithCircuitBreakers(registry *circuitbreaker.Registry) Option {
	return func(handler *Handler) {
		if registry != nil {
			handler.circuitBreakers = registry
		}
	}
}

// NewHandler creates a single-attempt forwarder using slog.Default unless overridden.
func NewHandler(options ...Option) *Handler {
	handler := &Handler{
		clients:             newClientCache(),
		logger:              slog.Default(),
		retryWaiter:         waitForRetry,
		maxResponseBodySize: defaultMaxResponseBodySize,
	}
	for _, option := range options {
		option(handler)
	}
	return handler
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ClientFor returns the shared client for a target's effective transport configuration.
func (handler *Handler) ClientFor(target targetgroup.Target, maxIdleConnsPerHost int) *http.Client {
	return handler.clients.get(target, maxIdleConnsPerHost)
}

// Forward buffers the inbound body once, then performs exactly one outbound attempt.
func (handler *Handler) Forward(writer http.ResponseWriter, request *http.Request, input ForwardInput) (AttemptResult, error) {
	body, err := BufferRequestBody(request)
	if err != nil {
		return AttemptResult{ErrClass: ClassifyError(err)}, err
	}
	return handler.ForwardBuffered(writer, request, BufferedAttempt{Input: input, Body: body})
}

// ForwardBuffered performs one outbound attempt with a body that a retry wrapper can replay.
func (handler *Handler) ForwardBuffered(writer http.ResponseWriter, request *http.Request, attempt BufferedAttempt) (result AttemptResult, err error) {
	input := attempt.Input
	if input.Group == nil {
		return AttemptResult{ErrClass: ErrClassOther}, ErrNilTargetGroup
	}
	rewrittenPath, exists := input.Match.RoutedPathMap[string(input.Group.ID())]
	if !exists {
		return AttemptResult{ErrClass: ErrClassOther}, fmt.Errorf("target group %q: %w", input.Group.ID(), ErrMissingRoutePath)
	}
	endpoint := &url.URL{Scheme: input.Target.Scheme(), Host: input.Target.Address(), Path: rewrittenPath, RawQuery: request.URL.RawQuery}
	outbound, err := http.NewRequestWithContext(request.Context(), request.Method, endpoint.String(), attempt.Body.Open())
	if err != nil {
		return handler.failed(request.Context(), result, "build upstream request", err)
	}
	outbound.Header = filteredRequestHeaders(request.Header)
	(&httputil.ProxyRequest{In: request, Out: outbound}).SetXForwarded()
	outbound.Header.Set(headers.TraceID, telemetry.TraceID(request.Context()))
	outbound.ContentLength = int64(len(attempt.Body.contents))
	response, err := handler.ClientFor(input.Target, input.Group.MaxIdleConnsPerHost()).Do(outbound) //nolint:gosec // target is validated gateway configuration, not request-controlled input
	if err != nil {
		return handler.failed(request.Context(), result, "execute upstream request", err)
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			if err == nil {
				result.ErrClass = ClassifyError(closeErr)
			}
			err = errors.Join(err, fmt.Errorf("close upstream response: %w", closeErr))
		}
	}()
	responseBody, err := readResponseBody(response.Body, handler.maxResponseBodySize)
	if err != nil {
		return handler.failed(request.Context(), result, "read upstream response", err)
	}
	if err := request.Context().Err(); err != nil {
		return handler.failed(request.Context(), result, "write response after client disconnect", err)
	}
	copyForwardableHeaders(writer.Header(), filteredResponseHeaders(response.Header))
	writer.WriteHeader(response.StatusCode)
	if _, err := writer.Write(responseBody); err != nil {
		return handler.failed(request.Context(), result, "write upstream response", err)
	}
	result.StatusCode = response.StatusCode
	return result, nil
}

// ClassifyError returns the retry-relevant class for a failed outbound operation.
func ClassifyError(err error) ErrClass {
	if err == nil {
		return ErrClassNone
	}
	if errors.Is(err, ErrResponseTooLarge) {
		return ErrClassOther
	}
	if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
		return ErrClassTimeout
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return ErrClassTimeout
	}
	return ErrClassOther
}

func (handler *Handler) failed(ctx context.Context, result AttemptResult, operation string, err error) (AttemptResult, error) {
	result.ErrClass = ClassifyError(err)
	if errors.Is(ctx.Err(), context.Canceled) {
		handler.logger.WarnContext(ctx, "proxy client disconnected", slog.Int("status", clientClosedStatus), slog.String("trace_id", telemetry.TraceID(ctx)), slog.Any("err", err))
	}
	return result, fmt.Errorf("%s: %w", operation, err)
}
