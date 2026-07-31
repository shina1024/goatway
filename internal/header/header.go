// Package header defines the gateway's custom HTTP header names.
//
// The names keep the "Goatway-" vendor prefix so they never collide with
// client or upstream headers, but drop the deprecated "X-" prefix that
// RFC 6648 advises against for newly defined headers.
package header

const (
	// TraceID carries the request trace identifier end to end.
	TraceID = "Goatway-Trace-ID"
	// APIToken carries the client API token used for gateway authentication.
	APIToken = "Goatway-API-Token" //nolint:gosec // this is a header name, not a credential
	// RequestTime carries the development-only request timestamp override.
	RequestTime = "Goatway-Request-Time"
)
