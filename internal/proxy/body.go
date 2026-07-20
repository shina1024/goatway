package proxy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
)

var ErrNilRequest = errors.New("proxy: nil request")

// BufferedBody retains a request body so every proxy attempt can open a fresh reader.
type BufferedBody struct {
	contents []byte
}

// BufferRequestBody reads the entire body into memory. It deliberately has no size
// limit so a future retry can replay every request; callers must enforce limits upstream.
func BufferRequestBody(request *http.Request) (BufferedBody, error) {
	if request == nil {
		return BufferedBody{}, ErrNilRequest
	}
	if request.Body == nil {
		return BufferedBody{}, nil
	}
	contents, readErr := io.ReadAll(request.Body)
	closeErr := request.Body.Close()
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
