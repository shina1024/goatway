package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestRuntime_HTTPHandler_truncatesUntrustedStringAttributesByRuneCount(t *testing.T) {
	// Given
	exporter := &memoryExporter{}
	runtime := newRuntimeWithMemoryExporter(t, exporter)
	path := "/" + strings.Repeat("界", 160)
	userAgent := strings.Repeat("🦫", 160)
	handler := runtime.HTTPHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	request := httptest.NewRequest(http.MethodGet, "http://gateway.example.test"+path, nil)
	request.Header.Set("User-Agent", userAgent)

	// When
	handler.ServeHTTP(httptest.NewRecorder(), request)
	shutdownRuntime(t, runtime)

	// Then
	serverSpan := exportedServerSpan(t, exporter.exportedSpans())
	wantPath := "/" + strings.Repeat("界", 127)
	wantUserAgent := strings.Repeat("🦫", 128)
	for key, want := range map[string]string{"url.path": wantPath, "user_agent.original": wantUserAgent} {
		got := spanStringAttribute(t, serverSpan, key)
		require.Equal(t, want, got)
		require.Equal(t, 128, utf8.RuneCountInString(got))
		require.True(t, utf8.ValidString(got))
	}
}

func TestRuntime_limitsSpanAttributeCount(t *testing.T) {
	// Given
	exporter := &memoryExporter{}
	runtime := newRuntimeWithMemoryExporter(t, exporter)
	_, span := runtime.TracerProvider().Tracer("telemetry-test").Start(context.Background(), "test.span")
	for index := range 33 {
		span.SetAttributes(attribute.String(fmt.Sprintf("attribute.%d", index), "value"))
	}

	// When
	span.End()
	shutdownRuntime(t, runtime)

	// Then
	exported := exporter.exportedSpans()
	require.Len(t, exported, 1)
	require.Len(t, exported[0].Attributes(), 32)
	require.Equal(t, 1, exported[0].DroppedAttributes())
	require.Empty(t, spanStringAttribute(t, exported[0], "attribute.32"))
}

func shutdownRuntime(t *testing.T, runtime *Runtime) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, runtime.Shutdown(ctx))
}

func exportedServerSpan(t *testing.T, spans []sdktrace.ReadOnlySpan) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, span := range spans {
		if span.SpanKind() == trace.SpanKindServer && span.Name() == "goatway.request" {
			return span
		}
	}
	t.Fatal("exported server span not found")
	return nil
}

func spanStringAttribute(t *testing.T, span sdktrace.ReadOnlySpan, key string) string {
	t.Helper()
	for _, candidate := range span.Attributes() {
		if candidate.Key == attribute.Key(key) {
			return candidate.Value.AsString()
		}
	}
	return ""
}
