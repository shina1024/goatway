package proxy

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"goatway/internal/circuitbreaker"
	"goatway/internal/httperr"
	"goatway/internal/router"
	"goatway/internal/targetgroup"
)

// RetryInput identifies the fetched group and route metadata for a retried transfer.
type RetryInput struct {
	Group *targetgroup.TargetGroup
	Match router.Match
}

type bufferedResponse struct {
	header     http.Header
	statusCode int
	body       bytes.Buffer
	wroteHead  bool
}

func newBufferedResponse() *bufferedResponse {
	return &bufferedResponse{header: make(http.Header), statusCode: http.StatusOK}
}

func (response *bufferedResponse) Header() http.Header {
	return response.header
}

func (response *bufferedResponse) WriteHeader(statusCode int) {
	if response.wroteHead {
		return
	}
	response.statusCode = statusCode
	response.wroteHead = true
}

func (response *bufferedResponse) Write(body []byte) (int, error) {
	if !response.wroteHead {
		response.WriteHeader(http.StatusOK)
	}
	return response.body.Write(body)
}

func (response *bufferedResponse) writeTo(writer http.ResponseWriter) error {
	copyForwardableHeaders(writer.Header(), response.header)
	writer.WriteHeader(response.statusCode)
	if _, err := writer.Write(response.body.Bytes()); err != nil {
		return fmt.Errorf("write selected upstream response: %w", err)
	}
	return nil
}

// ForwardWithRetry buffers one request body and retries eligible failures before selecting a response.
func (handler *Handler) ForwardWithRetry(writer http.ResponseWriter, request *http.Request, input RetryInput) (result AttemptResult, err error) {
	transferContext, transfer := handler.clients.telemetry.provider.Tracer("goatway/internal/proxy").Start(
		request.Context(), "goatway.proxy.transfer", trace.WithSpanKind(trace.SpanKindInternal),
	)
	request = request.WithContext(transferContext)
	attemptCount := 0
	targetGroup := ""
	if input.Group != nil {
		targetGroup = string(input.Group.ID())
	}
	defer func() {
		attributes := []attribute.KeyValue{attribute.Int("goatway.proxy.attempt_count", attemptCount)}
		if input.Group != nil {
			attributes = append(attributes, attribute.String("goatway.proxy.target_group.id", string(input.Group.ID())))
		}
		if input.Match.ClientType != "" {
			attributes = append(attributes, attribute.String("goatway.proxy.client.type", string(input.Match.ClientType)))
		}
		if result.StatusCode != 0 {
			attributes = append(attributes, attribute.Int("http.response.status_code", result.StatusCode))
		}
		if err != nil || result.ErrClass != ErrClassNone || result.StatusCode >= http.StatusInternalServerError {
			errorType := "other"
			if result.ErrClass == ErrClassTimeout {
				errorType = "timeout"
			} else if err == nil && result.StatusCode >= http.StatusInternalServerError {
				errorType = "server_error"
			}
			attributes = append(attributes, attribute.String("error.type", errorType))
			transfer.SetStatus(codes.Error, "")
			if handler.metrics != nil {
				handler.metrics.Errors.Add(request.Context(), 1, metric.WithAttributes(
					attribute.String("error_type", errorType),
					attribute.String("target_group", targetGroup),
				))
			}
		}
		transfer.SetAttributes(attributes...)
		transfer.End()
	}()
	if input.Group == nil {
		return AttemptResult{ErrClass: ErrClassOther}, ErrNilTargetGroup
	}
	body, err := BufferRequestBody(request)
	if err != nil {
		return AttemptResult{ErrClass: ClassifyError(err)}, err
	}
	attempts := retrySchedule(input.Group, request.Method)
	skipped := 0
	lastResult := AttemptResult{}
	var lastResponse *bufferedResponse
	var lastAttemptErr error
	for index, planned := range attempts {
		targetGroup = string(planned.group.ID())
		var breaker *circuitbreaker.Breaker
		if handler.circuitBreakers != nil {
			breaker = handler.circuitBreakers.Breaker(targetGroup)
			if breaker != nil && !breaker.Allow() {
				skipped++
				continue
			}
		}
		attemptCount++
		response := newBufferedResponse()
		result, attemptErr := handler.ForwardBuffered(response, request, BufferedAttempt{
			Input: ForwardInput{
				Target: planned.target,
				Group:  planned.group,
				Match:  input.Match,
			},
			Body: body,
		})
		if breaker != nil {
			recordCircuitBreakerOutcome(breaker, result, attemptErr)
		}
		lastResult = result
		lastResponse = response
		lastAttemptErr = attemptErr
		if retryable(planned.group, result, attemptErr) && index+1 < len(attempts) {
			if handler.metrics != nil {
				handler.metrics.Retries.Add(request.Context(), 1, metric.WithAttributes(attribute.String("target_group", targetGroup)))
			}
			next := attempts[index+1]
			delay := fullJitter(retryBackoffCap(next.group.RetryBaseInterval(), next.group.RetryMaxInterval(), index+1))
			if waitErr := handler.retryWaiter(request.Context(), delay); waitErr != nil {
				result.StatusCode = clientClosedStatus
				return handler.failed(request.Context(), result, "wait before retry", waitErr)
			}
			continue
		}
		if attemptErr != nil {
			status := http.StatusBadGateway
			if result.ErrClass == ErrClassTimeout {
				status = http.StatusGatewayTimeout
			}
			httperr.Write(writer, request.Context(), status, httperr.Code(status))
			result.StatusCode = status
			return result, attemptErr
		}
		if err := response.writeTo(writer); err != nil {
			return handler.failed(request.Context(), result, "write selected retry response", err)
		}
		return result, nil
	}
	if len(attempts) > 0 && skipped == len(attempts) {
		httperr.Write(writer, request.Context(), http.StatusServiceUnavailable, httperr.Code(http.StatusServiceUnavailable))
		return AttemptResult{StatusCode: http.StatusServiceUnavailable}, nil
	}
	if lastResponse != nil {
		if lastAttemptErr != nil {
			status := http.StatusBadGateway
			if lastResult.ErrClass == ErrClassTimeout {
				status = http.StatusGatewayTimeout
			}
			httperr.Write(writer, request.Context(), status, httperr.Code(status))
			lastResult.StatusCode = status
			return lastResult, lastAttemptErr
		}
		if err := lastResponse.writeTo(writer); err != nil {
			return handler.failed(request.Context(), lastResult, "write selected retry response", err)
		}
		return lastResult, nil
	}
	return AttemptResult{ErrClass: ErrClassOther}, fmt.Errorf("schedule retries for target group %q: no targets", input.Group.ID())
}

func recordCircuitBreakerOutcome(breaker *circuitbreaker.Breaker, result AttemptResult, attemptErr error) {
	if result.breakerFailure || result.ErrClass == ErrClassTimeout || (result.StatusCode >= http.StatusInternalServerError && result.StatusCode < 600) {
		breaker.RecordFailure()
		return
	}
	if errors.Is(attemptErr, ErrResponseTooLarge) {
		breaker.RecordSuccess()
		return
	}
	if attemptErr == nil && result.StatusCode < http.StatusInternalServerError {
		breaker.RecordSuccess()
	}
}
