package cache

import (
	"context"
	"errors"
	"time"

	"github.com/sanke08/api_gateway/internal/pkg/cacheclient"
)

// HybridStore combines a remote cache client with a local in-memory fallback.
//
// Why this exists:
// The gateway should still function if the remote cache service is temporarily
// unavailable. The local cache provides a safe fallback.
//
// Behavior:
// - Get tries remote first, then local fallback
// - Set tries remote, then always updates local
// - Delete tries remote, then always updates local
//
// Why this is useful:
// It keeps the gateway resilient without forcing the rest of the system to know
// which cache backend is currently active.
type HybridStore struct {
	remote cacheclient.Client
	local  *MemoryStore
}

// NewHybridStore creates a hybrid cache store.
//
// Why this constructor exists:
// It wires the remote client and the fallback store into one simple unit.
func NewHybridStore(remote cacheclient.Client, local *MemoryStore) *HybridStore {
	if local == nil {
		local = NewMemoryStore()
	}

	return &HybridStore{
		remote: remote,
		local:  local,
	}
}

// Get reads a value from the remote cache first, then falls back to memory.
//
// Why this order exists:
// The remote cache is the shared source of truth when it is available.
// The local cache only becomes the fallback when the remote cache is down
// or the key is not present there.
func (h *HybridStore) Get(ctx context.Context, key string) ([]byte, bool) {
	if ctx != nil && ctx.Err() != nil {
		return nil, false
	}

	if h.remote != nil {
		value, err := h.remote.Get(ctx, key)
		if err == nil {
			if h.local != nil {
				h.local.Set(ctx, key, value, 30*time.Second)
			}
			return value, true
		}

		// If the remote cache is unavailable, we fall back to local memory.
		// If the remote cache simply does not have the key, local memory may
		// still have a recent copy.
		if !errors.Is(err, cacheclient.ErrNotFound) {
			// ignore remote failure and continue to fallback
		}
	}

	if h.local == nil {
		return nil, false
	}

	return h.local.Get(ctx, key)
}

// Set writes a value to the remote cache and then updates the local fallback.
//
// Why this order exists:
// The remote cache should be updated first when available, but the local fallback
// should also receive the value so requests can still benefit from it if remote
// access is temporarily lost.
func (h *HybridStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) {
	if ctx != nil && ctx.Err() != nil {
		return
	}

	if h.remote != nil {
		_ = h.remote.Set(ctx, key, value, ttl)
	}

	if h.local != nil {
		h.local.Set(ctx, key, value, ttl)
	}
}

// Delete removes a key from both remote cache and local fallback.
//
// Why this exists:
// Invalidation should be applied everywhere we can reach.
func (h *HybridStore) Delete(ctx context.Context, key string) {
	if ctx != nil && ctx.Err() != nil {
		return
	}

	if h.remote != nil {
		_ = h.remote.Delete(ctx, key)
	}

	if h.local != nil {
		h.local.Delete(ctx, key)
	}
}
