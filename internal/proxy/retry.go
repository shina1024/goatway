package proxy

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"net/http"
	"time"

	"goatway/internal/router"
	"goatway/internal/targetgroup"
)

const (
	maxRetryIntervalMultiplier = 10
	maxDuration                = time.Duration(1<<63 - 1)
)

// RetryInput identifies the fetched group and route metadata for a retried transfer.
type RetryInput struct {
	Group   *targetgroup.TargetGroup
	Match   router.Match
	TraceID string
}

type retryAttempt struct {
	target targetgroup.Target
	group  *targetgroup.TargetGroup
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
	copyEndToEndHeaders(writer.Header(), response.header)
	writer.WriteHeader(response.statusCode)
	if _, err := writer.Write(response.body.Bytes()); err != nil {
		return fmt.Errorf("write selected upstream response: %w", err)
	}
	return nil
}

// ForwardWithRetry buffers one request body and retries eligible failures before selecting a response.
func (handler *Handler) ForwardWithRetry(writer http.ResponseWriter, request *http.Request, input RetryInput) (AttemptResult, error) {
	if input.Group == nil {
		return AttemptResult{ErrClass: ErrClassOther}, ErrNilTargetGroup
	}
	body, err := BufferRequestBody(request)
	if err != nil {
		return AttemptResult{ErrClass: ClassifyError(err)}, err
	}
	attempts := retrySchedule(input.Group, request.Method)
	traceID := input.TraceID
	for index, planned := range attempts {
		response := newBufferedResponse()
		result, attemptErr := handler.ForwardBuffered(response, request, BufferedAttempt{
			Input: ForwardInput{
				Target:  planned.target,
				Group:   planned.group,
				Match:   input.Match,
				TraceID: traceID,
			},
			Body: body,
		})
		traceID = result.TraceID
		if retryable(planned.group, result, attemptErr) && index+1 < len(attempts) {
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
			http.Error(writer, http.StatusText(status), status)
			result.StatusCode = status
			return result, attemptErr
		}
		if err := response.writeTo(writer); err != nil {
			return handler.failed(request.Context(), result, "write selected retry response", err)
		}
		return result, nil
	}
	return AttemptResult{ErrClass: ErrClassOther}, fmt.Errorf("schedule retries for target group %q: no targets", input.Group.ID())
}

func retrySchedule(group *targetgroup.TargetGroup, method string) []retryAttempt {
	maxTryCount := group.MaxTryCount()
	if !group.RetryNonIdempotent() && (method == http.MethodPost || method == http.MethodPatch) {
		maxTryCount = 1
	}
	if maxTryCount < 1 {
		return nil
	}
	if retryGroup := group.RetryToTargetGroup(); retryGroup != nil {
		first := group.ScheduledTargets(1)
		remaining := retryGroup.ScheduledTargets(maxTryCount - 1)
		attempts := make([]retryAttempt, 0, maxTryCount)
		for _, target := range first {
			attempts = append(attempts, retryAttempt{target: target, group: group})
		}
		for _, target := range remaining {
			attempts = append(attempts, retryAttempt{target: target, group: retryGroup})
		}
		return attempts
	}
	targets := group.ScheduledTargets(maxTryCount)
	attempts := make([]retryAttempt, len(targets))
	for index, target := range targets {
		attempts[index] = retryAttempt{target: target, group: group}
	}
	return attempts
}

func retryable(group *targetgroup.TargetGroup, result AttemptResult, attemptErr error) bool {
	var retryCase targetgroup.RetryCase
	if attemptErr != nil {
		if result.ErrClass == ErrClassTimeout {
			retryCase = targetgroup.RetryCaseTimeout
		}
	} else if result.StatusCode >= http.StatusInternalServerError && result.StatusCode < 600 {
		retryCase = targetgroup.RetryCaseServerError
	}
	for _, configured := range group.RetryCases() {
		if configured == retryCase {
			return true
		}
	}
	return false
}

func retryBackoffCap(baseInterval, maxInterval time.Duration, tryCount int) time.Duration {
	if baseInterval <= 0 {
		return 0
	}
	if maxInterval <= 0 {
		if baseInterval > maxDuration/maxRetryIntervalMultiplier {
			maxInterval = maxDuration
		} else {
			maxInterval = maxRetryIntervalMultiplier * baseInterval
		}
	}
	if baseInterval >= maxInterval {
		return maxInterval
	}
	interval := baseInterval
	for range tryCount {
		if interval >= maxInterval || interval > maxInterval/2 {
			return maxInterval
		}
		interval *= 2
	}
	return interval
}

func fullJitter(cap time.Duration) time.Duration {
	milliseconds := cap / time.Millisecond
	if milliseconds <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(milliseconds)+1)) * time.Millisecond
}
