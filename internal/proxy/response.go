package proxy

import (
	"errors"
	"io"
)

var ErrResponseTooLarge = errors.New("proxy: response body too large")

func readResponseBody(body io.Reader, limit int64) ([]byte, error) {
	contents, err := io.ReadAll(io.LimitReader(body, limit))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) < limit {
		return contents, nil
	}
	extra, err := io.ReadAll(io.LimitReader(body, 1))
	if err != nil {
		return nil, err
	}
	if len(extra) > 0 {
		return nil, ErrResponseTooLarge
	}
	return contents, nil
}
