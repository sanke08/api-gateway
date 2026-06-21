package cache

import (
	"context"
	"sync"
	"time"
)

// MemoryStore
// │
// ├── items map
// │
// ├── user:123
// │     ├── value = {"name":"john"}
// │     ├── expiresAt = 10:05
// │     └── hasExpiry = true
// │
// ├── user:456
// │     ├── value = {"name":"alice"}
// │     ├── expiresAt = 10:10
// │     └── hasExpiry = true
// │
// └── tenant:1
//       ├── value = {...}
//       ├── expiresAt = zero
//       └── hasExpiry = false

// Store is the local cache contract.
//
// Why this exists:
// The gateway may need a safe fallback when the remote cache is down.
// This interface keeps the fallback implementation simple and testable.
// Store defines the operations any cache implementation must support.
//
// Why this exists:
//
// The gateway should depend on behavior, not implementation.
//
// Today:
//
//	MemoryStore
//
// Tomorrow:
//
//	RedisStore
//	RemoteCacheStore
//	MemcachedStore
//
// As long as they implement Store,
// the gateway code does not need to change.
//
// Think:
//
// Store = contract
// MemoryStore = one implementation
//
// Example:
//
//	var cache Store
//
//	cache = NewMemoryStore()
//
// Later:
//
//	cache = NewRedisStore()
//
// Gateway code remains exactly the same.
type Store interface {

	// Get retrieves a value from cache.
	Get(ctx context.Context, key string) ([]byte, bool)

	// Set stores a value in cache.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration)

	// Delete removes a key from cache.
	Delete(ctx context.Context, key string)

	// PruneExpired removes expired entries.
	//
	// Example:
	//
	//	user:123 expired
	//	user:456 expired
	//
	// Returns:
	//
	//	2
	//
	// Meaning:
	//
	//	2 expired items were removed.
	PruneExpired() int
}

// MemoryStore is a concurrency-safe in-memory cache.
//
// Why this exists:
// It gives the gateway a fallback cache when the remote cache cannot be reached.
// MemoryStore is an in-memory cache implementation.
//
// Think:
//
// RAM
//
//	|
//	+-- user:123 -> data
//	+-- user:456 -> data
//	+-- tenant:1 -> data
//
// Everything lives inside the gateway process memory.
//
// Why this exists:
//
// If the remote cache service fails:
//
//	Gateway
//	    |
//	    X
//	    |
//	Cache Service
//
// the gateway can still cache some data locally.
//
// Why RWMutex is used:
//
// Multiple requests may access cache simultaneously.
//
// Example:
//
//	Request 1 -> Get()
//	Request 2 -> Get()
//	Request 3 -> Set()
//
// RWMutex protects the map from concurrent access crashes.
//
// Why items is a map:
//
// Fast O(1) lookup.
//
// Example:
//
//	items["user:123"]
//
// immediately returns the cached value.
//
// Why clock is injected:
//
// Production:
//
//	time.Now()
//
// Tests:
//
//	fakeTime.Now()
//
// Makes expiration logic easy to test without waiting in real time.
type MemoryStore struct {
	mu    sync.RWMutex
	items map[string]memoryItem
	clock func() time.Time
}

// memoryItem holds one cached value and its expiration time.
//
// Why this exists:
// Every cache entry needs to know when it expires.
// memoryItem represents one cache entry.
//
// Example:
//
//	user:123 -> {
//	    value: "john",
//	    expiresAt: 10:30 AM,
//	    hasExpiry: true,
//	}
//
// Why this struct exists:
//
// A cache entry needs more than just data.
//
// It also needs expiration metadata so the cache knows
// when the value should disappear.
type memoryItem struct {

	// value is the actual cached data.
	//
	// Example:
	//
	//	[]byte("john")
	//	[]byte(`{"id":123}`)
	value []byte

	// expiresAt is when this cache entry becomes invalid.
	//
	// Example:
	//
	//	now = 10:00
	//	ttl = 5 minutes
	//
	//	expiresAt = 10:05
	//
	// After 10:05:
	//
	// this entry should no longer be returned.
	expiresAt time.Time

	// hasExpiry indicates whether expiration should be enforced.
	//
	// Example:
	//
	//	hasExpiry = true
	//
	// Means:
	//
	//	Check expiresAt.
	//
	// Example:
	//
	//	hasExpiry = false
	//
	// Means:
	//
	//	Never expires automatically.
	hasExpiry bool
}

// NewMemoryStore creates a new in-memory cache.
//
// Example:
//
//	cache := NewMemoryStore()
//
// Internally:
//
//	MemoryStore{
//	    items: make(map[string]memoryItem),
//	    clock: time.Now,
//	}
//
// Why this exists:
//
// Provides a simple production-ready constructor.
//
// Most callers do not care about custom clocks.
//
// They simply want:
//
//	cache := NewMemoryStore()
//
// and start storing values.
func NewMemoryStore() *MemoryStore {
	return NewMemoryStoreWithClock(time.Now)
}

// NewMemoryStoreWithClock creates an in-memory cache with a custom clock.
//
// Why this exists:
// Tests can inject a fake clock and advance time deterministically.
func NewMemoryStoreWithClock(clock func() time.Time) *MemoryStore {
	if clock == nil {
		clock = time.Now
	}

	return &MemoryStore{
		items: make(map[string]memoryItem),
		clock: clock,
	}
}

// Get reads one value from memory.
//
// What it returns:
// - value: cached bytes
// - ok: true if the key exists and is not expired
func (s *MemoryStore) Get(ctx context.Context, key string) ([]byte, bool) {
	if ctx != nil && ctx.Err() != nil {
		return nil, false
	}

	now := s.now()

	s.mu.RLock()
	item, ok := s.items[key]
	s.mu.RUnlock()

	if !ok {
		return nil, false
	}

	if item.hasExpiry && now.After(item.expiresAt) {
		s.mu.Lock()
		delete(s.items, key)
		s.mu.Unlock()
		return nil, false
	}

	value := make([]byte, len(item.value))
	copy(value, item.value)

	return value, true
}

// Set stores one value in memory with optional expiration.
func (s *MemoryStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) {
	if ctx != nil && ctx.Err() != nil {
		return
	}

	item := memoryItem{
		value: make([]byte, len(value)),
	}
	copy(item.value, value)

	if ttl > 0 {
		item.hasExpiry = true
		item.expiresAt = s.now().Add(ttl)
	}

	s.mu.Lock()
	s.items[key] = item
	s.mu.Unlock()
}

// Delete removes one key from memory.
func (s *MemoryStore) Delete(ctx context.Context, key string) {
	if ctx != nil && ctx.Err() != nil {
		return
	}

	s.mu.Lock()
	delete(s.items, key)
	s.mu.Unlock()
}

// PruneExpired removes expired entries and returns how many were removed.
//
// Why this exists:
// The fallback cache should not keep dead entries forever.
func (s *MemoryStore) PruneExpired() int {
	now := s.now()
	removed := 0

	s.mu.Lock()
	defer s.mu.Unlock()

	for key, item := range s.items {
		if item.hasExpiry && now.After(item.expiresAt) {
			delete(s.items, key)
			removed++
		}
	}

	return removed
}

// now returns the current time.
//
// Why this exists:
// It keeps time access centralized and testable.
func (s *MemoryStore) now() time.Time {
	if s.clock == nil {
		return time.Now()
	}
	return s.clock()
}
