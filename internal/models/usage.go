package models

import "time"

// UsageRecord stores request-level usage data.
//
// Why this exists:
// This will later support metering, analytics, billing preparation, debugging,
// and tenant-level traffic visibility.
// Usage represents API usage tracking for metrics.
type Usage struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	APIKeyID string `json:"api_key_id"`
	Endpoint string `json:"endpoint"`
	Method   string `json:"method"`

	BytesIn  int64
	BytesOut int64

	Timestamp time.Time `json:"timestamp"`
}
