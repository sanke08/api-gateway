package models

import "time"

// UsageRecord stores one request-level usage event.
//
// Why this struct exists:
// The gateway needs durable traffic data for billing preparation, tenant analytics,
// and request auditing.
//
// Why the fields are explicit:
// Usage data is only useful if we can answer real questions later:
// - who sent the request?
// - which tenant owned it?
// - was it cached?
// - was it retried?
// - how long did it take?
// - how many bytes moved?
//
// Important design choice:
// Some identity fields are optional because not every request comes through the
// same authentication path. For example:
// - human login requests may not have an API key
// - machine requests may not have a user membership
type Usage struct {
	// ID is the database identifier of this usage row.
	ID string

	// RequestID is the request correlation identifier.
	//
	// Why this exists:
	// It lets operators connect usage rows back to traces and logs.
	RequestID string

	// TenantID identifies which tenant generated the traffic.
	TenantID string

	// APIKeyID identifies the machine credential used for the request.
	//
	// Why this is a pointer:
	// Human-authenticated requests may not use an API key.
	APIKeyID *string

	// UserID identifies the human user who triggered the request.
	//
	// Why this is a pointer:
	// Machine requests may not have a human user attached.
	UserID *string

	// MembershipID identifies the tenant membership used for the request.
	//
	// Why this is a pointer:
	// The same user can belong to multiple tenants, so membership must remain optional
	// in the usage record.
	MembershipID *string

	// Path is the incoming request path.
	Path string

	// Method is the incoming HTTP method.
	Method string

	// StatusCode is the final HTTP status returned to the client.
	StatusCode int

	// DurationMS is the total request duration in milliseconds.
	DurationMS int64

	// Timestamp is the moment the gateway recorded this usage row.
	Timestamp time.Time

	// BytesIn is the number of bytes the request carried in.
	BytesIn int64

	// BytesOut is the number of bytes returned to the client.
	BytesOut int64

	// Cached reports whether the request was served from cache.
	Cached bool

	// Retried reports whether the request needed retry behavior.
	Retried bool
}
