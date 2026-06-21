package observability

// RecordCacheHit exposes a simple helper for later cache middleware wiring.
//
// Why this exists:
// Future middleware can call the registry directly, but a small helper keeps
// call sites easy to read.
func RecordCacheHit(reg *Registry, tenantID string, route string) {
	if reg == nil {
		return
	}

	reg.RecordCacheHit(Labels{
		"tenant": tenantID,
		"route":  route,
	})
}

// RecordCacheMiss exposes a simple helper for later cache middleware wiring.
func RecordCacheMiss(reg *Registry, tenantID string, route string) {
	if reg == nil {
		return
	}

	reg.RecordCacheMiss(Labels{
		"tenant": tenantID,
		"route":  route,
	})
}
