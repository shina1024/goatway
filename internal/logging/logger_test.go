package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNew_devReturnsTextHandler(t *testing.T) {
	var buf bytes.Buffer
	logger := New("dev", &buf)
	logger.Info("hello")

	output := buf.String()
	if !strings.Contains(output, "level=INFO") || !strings.Contains(output, "msg=hello") {
		t.Errorf("dev logger output = %q, want text format with level=INFO msg=hello", output)
	}
}

func TestNew_nonDevReturnsJSONHandler(t *testing.T) {
	var buf bytes.Buffer
	logger := New("prod", &buf)
	logger.Info("hello")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("prod logger output is not valid JSON: %v\noutput: %q", err, buf.String())
	}
	if entry["msg"] != "hello" {
		t.Errorf("prod logger msg = %v, want hello", entry["msg"])
	}
}
