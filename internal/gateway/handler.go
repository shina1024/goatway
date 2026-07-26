package gateway

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"goatway/internal/config"
	"goatway/internal/headers"
	"goatway/internal/proxy"
	"goatway/internal/router"
	"goatway/internal/targetgroup"
	"goatway/internal/telemetry"
	"goatway/internal/throttle"
)

// Handler composes route authorization, throttling, and upstream forwarding.
type Handler struct {
	configuration *config.Config
	registry      *targetgroup.Registry
	routes        *router.Router
	limiter       *throttle.Limiter
	tracker       *throttle.DeploymentTracker
	proxy         *proxy.Handler
	logger        *slog.Logger
	devMode       bool
	metrics       *telemetry.Metrics
}

// Option configures a Handler dependency.
type Option func(*Handler)

// WithProxy sets the forwarder used for upstream attempts.
func WithProxy(forwarder *proxy.Handler) Option {
	return func(handler *Handler) {
		handler.proxy = forwarder
	}
}

// WithLogger sets the structured logger used for gateway decisions.
func WithLogger(logger *slog.Logger) Option {
	return func(handler *Handler) {
		handler.logger = logger
	}
}

// WithDevMode enables development-only features such as request-time override.
func WithDevMode(devMode bool) Option {
	return func(handler *Handler) {
		handler.devMode = devMode
	}
}

// WithMetrics sets the RED metric instruments used by the gateway.
func WithMetrics(metrics *telemetry.Metrics) Option {
	return func(handler *Handler) {
		handler.metrics = metrics
	}
}

// NewHandler creates the top-level gateway HTTP handler.
func NewHandler(
	configuration *config.Config,
	registry *targetgroup.Registry,
	routes *router.Router,
	limiter *throttle.Limiter,
	tracker *throttle.DeploymentTracker,
	options ...Option,
) *Handler {
	handler := &Handler{
		configuration: configuration,
		registry:      registry,
		routes:        routes,
		limiter:       limiter,
		tracker:       tracker,
		logger:        slog.Default(),
	}
	for _, option := range options {
		if option != nil {
			option(handler)
		}
	}
	if handler.configuration == nil || handler.registry == nil || handler.routes == nil || handler.limiter == nil || handler.tracker == nil {
		panic("gateway: nil dependency passed to NewHandler")
	}
	if handler.logger == nil {
		handler.logger = slog.Default()
	}
	if handler.proxy == nil {
		handler.proxy = proxy.NewHandler(proxy.WithLogger(handler.logger))
	}
	return handler
}

// ServeHTTP applies the gateway request flow in its required order.
func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if handler == nil || request == nil || handler.configuration == nil || handler.registry == nil || handler.routes == nil || handler.limiter == nil || handler.tracker == nil || handler.proxy == nil {
		http.Error(writer, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	started := time.Now()
	requestContext := request.Context()
	requestMethod := request.Method
	metricsWriter := &metricsResponseWriter{ResponseWriter: writer}
	client := ""
	targetGroup := ""
	defer func() {
		handler.recordRequestMetrics(requestContext, started, requestMethod, metricsWriter.status, client, targetGroup)
	}()
	writer = metricsWriter

	writer.Header().Set(headers.TraceID, telemetry.TraceID(request.Context()))
	originalRequest := request
	request, err := router.WithRequestTimeOverride(request, handler.devMode)
	if err != nil {
		handler.logger.WarnContext(originalRequest.Context(), "gateway request time rejected", slog.String("trace_id", telemetry.TraceID(originalRequest.Context())), slog.Any("err", err))
		handler.respond(writer, originalRequest, http.StatusBadRequest)
		return
	}

	match, err := handler.routes.Route(request)
	if err != nil {
		handler.writeRouteError(writer, request, err)
		return
	}
	client = string(match.ClientType)
	targetGroup = match.TargetGroupID
	handler.logger.InfoContext(
		request.Context(), "gateway route matched",
		slog.String("trace_id", telemetry.TraceID(request.Context())),
		slog.String("target_group", match.TargetGroupID),
		slog.String("client", string(match.ClientType)),
	)

	if match.ClientType != "" {
		client := string(match.ClientType)
		count := handler.limiter.Inc(client)
		defer handler.limiter.Dec(client)
		deploymentState := handler.tracker.GetDeploymentState()
		instanceCounts := deploymentState.InstanceCounts
		trafficWeight := deploymentState.TrafficWeight
		depType := handler.tracker.GetDepType()
		if handler.limiter.IsOverLimit(client, count, depType, instanceCounts, trafficWeight) {
			handler.recordThrottleRejection(request.Context(), client)
			handler.logger.WarnContext(
				request.Context(), "gateway throttle rejected",
				slog.String("trace_id", telemetry.TraceID(request.Context())),
				slog.String("client", client),
				slog.Int("count", count),
				slog.Int("maximum", handler.configuration.MaxConcurrentRequests[config.ClientType(client)]),
				slog.String("deployment_type", depType),
				slog.Int("primary_instances", instanceCounts.Primary),
				slog.Int("canary_instances", instanceCounts.Canary),
				slog.Int("primary_weight", trafficWeight.Primary),
				slog.Int("canary_weight", trafficWeight.Canary),
			)
			handler.respond(writer, request, http.StatusTooManyRequests)
			return
		}
	}

	group, err := handler.registry.Lookup(config.TargetGroupID(match.TargetGroupID))
	if err != nil {
		handler.logger.ErrorContext(request.Context(), "gateway target group lookup failed", slog.String("trace_id", telemetry.TraceID(request.Context())), slog.Any("err", err))
		handler.respond(writer, request, http.StatusInternalServerError)
		return
	}
	result, err := handler.proxy.ForwardWithRetry(writer, request, proxy.RetryInput{Group: group, Match: match})
	handler.logger.InfoContext(
		request.Context(), "gateway upstream attempt result",
		slog.String("trace_id", telemetry.TraceID(request.Context())),
		slog.Int("status", result.StatusCode),
		slog.Int("error_class", int(result.ErrClass)),
		slog.Any("err", err),
	)
	if err != nil && result.StatusCode == 0 {
		handler.logger.ErrorContext(request.Context(), "gateway upstream forwarding failed", slog.String("trace_id", telemetry.TraceID(request.Context())), slog.Any("err", err))
		handler.respond(writer, request, http.StatusBadGateway)
		return
	}
	handler.logger.InfoContext(request.Context(), "gateway response completed", slog.String("trace_id", telemetry.TraceID(request.Context())), slog.Int("status", result.StatusCode))
}

func (handler *Handler) writeRouteError(writer http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, router.ErrNoRoute):
		handler.logger.InfoContext(request.Context(), "gateway route not found", slog.String("trace_id", telemetry.TraceID(request.Context())))
		handler.respond(writer, request, http.StatusNotFound)
	case errors.Is(err, router.ErrMissingToken):
		handler.logger.WarnContext(request.Context(), "gateway authentication rejected", slog.String("trace_id", telemetry.TraceID(request.Context())), slog.Any("err", err))
		handler.respond(writer, request, http.StatusUnauthorized)
	case errors.Is(err, router.ErrUnknownToken), errors.Is(err, router.ErrClientNotAllowed):
		handler.logger.WarnContext(request.Context(), "gateway authentication rejected", slog.String("trace_id", telemetry.TraceID(request.Context())), slog.Any("err", err))
		handler.respond(writer, request, http.StatusForbidden)
	case errors.Is(err, router.ErrIPNotAllowed):
		handler.logger.WarnContext(request.Context(), "gateway IP authorization rejected", slog.String("trace_id", telemetry.TraceID(request.Context())), slog.Any("err", err))
		handler.respond(writer, request, http.StatusForbidden)
	default:
		handler.logger.ErrorContext(request.Context(), "gateway route failed", slog.String("trace_id", telemetry.TraceID(request.Context())), slog.Any("err", err))
		handler.respond(writer, request, http.StatusInternalServerError)
	}
}

func (handler *Handler) respond(writer http.ResponseWriter, request *http.Request, status int) {
	writer.WriteHeader(status)
	handler.logger.InfoContext(request.Context(), "gateway response completed", slog.String("trace_id", telemetry.TraceID(request.Context())), slog.Int("status", status))
}
