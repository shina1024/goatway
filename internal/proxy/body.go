package proxy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
)

var ErrNilRequest = errors.New("proxy: nil request")

const maxRequestBodySize = 10 << 20 // 10 MiB

// BufferedBody retains a request body so every proxy attempt can open a fresh reader.
type BufferedBody struct {
	contents []byte
}

// BufferRequestBody reads the entire body into memory, rejecting bodies that
// exceed maxRequestBodySize so a single request cannot exhaust gateway memory.
func BufferRequestBody(request *http.Request) (BufferedBody, error) {
	if request == nil {
		return BufferedBody{}, ErrNilRequest
	}
	if request.Body == nil {
		return BufferedBody{}, nil
	}
	limited := http.MaxBytesReader(nil, request.Body, maxRequestBodySize)
	contents, readErr := io.ReadAll(limited)
	closeErr := limited.Close()
	if readErr != nil {
		return BufferedBody{}, fmt.Errorf("read request body: %w", readErr)
	}
	if closeErr != nil {
		return BufferedBody{}, fmt.Errorf("close request body: %w", closeErr)
	}
	body := BufferedBody{contents: contents}
	request.Body = body.Open()
	request.GetBody = func() (io.ReadCloser, error) { return body.Open(), nil }
	request.ContentLength = int64(len(contents))
	return body, nil
}

// Open returns a new reader over the buffered bytes for one upstream attempt.
func (body BufferedBody) Open() io.ReadCloser {
	return io.NopCloser(bytes.NewReader(body.contents))
}
