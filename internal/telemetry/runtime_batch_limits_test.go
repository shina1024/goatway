package telemetry

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestRuntime_dropsSpansPastQueuedAndInFlightCapacity(t *testing.T) {
	// Given
	exporter := &blockingExporter{
		firstExport:   make(chan struct{}, 1),
		releaseExport: make(chan struct{}),
	}
	config := Config{Endpoint: "https://collector.example.test:4317", Protocol: grpcProtocol}
	runtime, err := newRuntime(context.Background(), config, func(context.Context, Config) (sdktrace.SpanExporter, error) {
		return exporter, nil
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		exporter.release()
		shutdownRuntime(t, runtime)
	})
	tracer := runtime.TracerProvider().Tracer("telemetry-test")
	for range 32 {
		_, span := tracer.Start(context.Background(), "in-flight")
		span.End()
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	select {
	case <-exporter.firstExport:
	case <-waitCtx.Done():
		t.Fatalf("first 32-span export did not start: %v", waitCtx.Err())
	}
	require.Equal(t, []int{32}, exporter.batchSizes())

	// When
	for range 129 {
		_, span := tracer.Start(context.Background(), "queued")
		span.End()
	}
	exporter.release()
	shutdownRuntime(t, runtime)

	// Then
	batchSizes := exporter.batchSizes()
	exported := 0
	for _, size := range batchSizes {
		require.LessOrEqual(t, size, 32)
		exported += size
	}
	require.Equal(t, 160, exported)
}

type blockingExporter struct {
	firstExport   chan struct{}
	releaseExport chan struct{}

	firstOnce   sync.Once
	releaseOnce sync.Once
	mu          sync.Mutex
	batchSize   []int
}

func (exporter *blockingExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	exporter.mu.Lock()
	exporter.batchSize = append(exporter.batchSize, len(spans))
	exporter.mu.Unlock()

	block := false
	exporter.firstOnce.Do(func() { block = true })
	if !block {
		return nil
	}
	select {
	case exporter.firstExport <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-exporter.releaseExport:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (*blockingExporter) Shutdown(context.Context) error {
	return nil
}

func (exporter *blockingExporter) release() {
	exporter.releaseOnce.Do(func() { close(exporter.releaseExport) })
}

func (exporter *blockingExporter) batchSizes() []int {
	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	return append([]int(nil), exporter.batchSize...)
}
