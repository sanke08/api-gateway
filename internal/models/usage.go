package models

import "time"

// Usage represents API usage tracking for metrics.
type Usage struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	APIKeyID string `json:"api_key_id"`
	Endpoint string `json:"endpoint"`
	Method   string `json:"method"`

	Timestamp time.Time `json:"timestamp"`
}
