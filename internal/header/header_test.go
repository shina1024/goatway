package header

import "testing"

// TestHeaderNames_pinWireContract locks the on-the-wire header names so an
// accidental rename surfaces here rather than silently breaking clients.
func TestHeaderNames_pinWireContract(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"trace ID", TraceID, "Goatway-Trace-ID"},
		{"API token", APIToken, "Goatway-API-Token"},
		{"request time", RequestTime, "Goatway-Request-Time"},
	}
	for _, test := range tests {
		if test.value != test.want {
			t.Errorf("%s header = %q, want %q", test.name, test.value, test.want)
		}
	}
}
