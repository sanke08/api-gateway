package cacheclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// ErrNotFound means the cache key does not exist in the remote cache service.
//
// Why this exists:
// The caller needs a stable way to distinguish "missing key" from transport
// failures or server failures.
// ErrNotFound means:
//
// "The cache service was reached successfully,
// but the requested key does not exist."
//
// Example:
//
// Cache contains:
//
//	user:1
//	user:2
//
// Request:
//
//	Get("user:999")
//
// Cache service response:
//
//	404 Not Found
//
// We convert that into:
//
//	ErrNotFound
//
// Why this matters:
//
// Callers often want different behavior for:
//
// 1. Key missing
// 2. Cache service failure
//
// Example:
//
// value, err := cache.Get(...)
//
//	if errors.Is(err, ErrNotFound) {
//	    // cache miss
//	}
//
//	if err != nil {
//	    // actual failure
//	}
//
// Without ErrNotFound every failure would look identical.
var ErrNotFound = errors.New("cache key not found")

// ErrInvalidKey means the caller tried to use an empty or invalid cache key.
//
// Why this exists:
// Cache operations should fail fast if the key is unusable.
// ErrInvalidKey means:
//
// "The caller provided a key that cannot be used."
//
// Example:
//
//	Get("")
//
//	Set("", value)
//
//	Delete("")
//
// These should fail immediately.
//
// Why fail early?
//
// Bad:
//
// Send HTTP request
// Cache service rejects it
//
// Better:
//
// # Detect bug immediately
//
// Example:
//
// User ID accidentally missing:
//
//	key := ""
//
// cache.Get(ctx, key)
//
// Result:
//
//	ErrInvalidKey
//
// instead of making a useless network call.
var ErrInvalidKey = errors.New("cache key is invalid")

// ErrInvalidTTL means the caller supplied a TTL that cannot be used.
//
// Why this exists:
// Cache entries should have a clear lifetime. A broken TTL should be rejected
// early rather than stored ambiguously.
// ErrInvalidTTL means:
//
// "The expiration duration is invalid."
//
// TTL = Time To Live
//
// Example:
//
// cache.Set(
//
//	ctx,
//	"user:1",
//	data,
//	10*time.Minute,
//
// )
//
// Meaning:
//
// Store data for 10 minutes.
//
// --------------------------------
//
// Invalid examples:
//
// ttl = 0
// ttl = -5 seconds
//
// These values usually indicate a bug.
//
// Why this exists:
//
// Expiration behavior should be explicit.
//
// We do not want entries living forever
// because of accidental configuration mistakes.
var ErrInvalidTTL = errors.New("cache ttl is invalid")

// Client defines the cache operations the gateway can use.
//
// Why this interface exists:
// The gateway should talk to cache through a small, explicit contract.
// It should not care whether the cache service is remote, local, or replaced later.
// Client is the contract the gateway depends on.
//
// Very important:
//
// Gateway depends on:
//
//	Behavior
//
// not
//
//	Implementation
//
// The gateway only knows:
//
//	Get()
//	Set()
//	Delete()
//
// It does NOT know:
//
// - Redis
// - Memcached
// - HTTP cache service
// - In-memory cache
//
// Example:
//
//	var cache Client
//
// Today:
//
//	cache = RemoteClient
//
// Tomorrow:
//
//	cache = RedisClient
//
// Next year:
//
//	cache = InMemoryClient
//
// Gateway code never changes.
//
// This is the main benefit of interfaces.
//
// Think:
//
// "What can you do?"
//
// rather than:
//
// "What are you?"
type Client interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// RemoteClient talks to a remote cache service over HTTP.
//
// Why this exists:
// Phase 16 is about cache client integration, not cache policy.
// This client is the bridge between the gateway and an external cache service.
// RemoteClient is a concrete implementation of Client.
//
// Unlike an in-memory cache:
//
//	map[string][]byte
//
// this client stores nothing locally.
//
// Instead it sends HTTP requests to another service.
//
// Example:
//
// Gateway
//
//	|
//	| GET /cache/user:123
//	|
//	v
//
// # Cache Service
//
// Why this exists:
//
// Large systems often run cache as a dedicated service.
//
// Examples:
//
// Gateway
// API Service
// Billing Service
//
// all share the same cache cluster.
//
// RemoteClient is simply the HTTP bridge
// between the gateway and that cache service.
type RemoteClient struct {
	// baseURL is the cache service address.
	//
	// Example:
	//
	// https://cache.internal
	//
	// or
	//
	// https://cache.company.com
	//
	// Why store it parsed?
	//
	// Parsing URLs repeatedly is wasteful.
	//
	// Bad:
	//
	// Every request:
	//
	//	url.Parse(...)
	//
	//
	// Better:
	//
	// Parse once during startup.
	//
	// Store the parsed URL.
	//
	// Reuse forever.
	baseURL *url.URL

	// httpClient performs the actual HTTP requests.
	//
	// Example:
	//
	// GET  /cache/user:123
	// POST /cache/user:123
	// DELETE /cache/user:123
	//
	// Why reuse one client?
	//
	// Creating a new http.Client per request
	// destroys connection reuse.
	//
	// Reusing one client allows:
	//
	// - keep-alive
	// - connection pooling
	// - lower latency
	// - fewer TCP handshakes
	//
	// Production rule:
	//
	// Create once.
	// Reuse forever.
	httpClient *http.Client

	// namespace logically separates keys.
	//
	// Example:
	//
	// Without namespace:
	//
	//	user:123
	//
	// could collide with:
	//
	//	another service's user:123
	//
	// --------------------------------
	//
	// With namespace:
	//
	//	gateway:user:123
	//
	//	billing:user:123
	//
	//	auth:user:123
	//
	// Now collisions disappear.
	//
	// Think of namespace like
	// a folder prefix for cache keys.
	namespace string

	// token is used to authenticate
	// with the cache service.
	//
	// Example:
	//
	// Authorization: Bearer abc123
	//
	// Why this exists:
	//
	// Cache services are often private.
	//
	// Not every caller should be allowed
	// to read or write cache entries.
	//
	// The token lets the cache service
	// verify who is making requests.
	token string
}

// NewRemoteClient creates a remote cache client.
//
// Example:
//
//	client, err := cacheclient.NewRemoteClient(
//	    "https://cache-service.internal",
//	    2*time.Second,
//	    "gateway",
//	    "",
//	)
//
// Why this constructor exists:
// It validates the cache endpoint and prepares a reusable HTTP client.
// NewRemoteClient builds a reusable cache client.
//
// Startup flow:
//
//	main()
//	  |
//	  v
//	NewRemoteClient()
//	  |
//	  v
//	validate configuration
//	  |
//	  v
//	create HTTP client
//	  |
//	  v
//	return ready-to-use client
//
// Why this exists:
//
// We want all validation during startup.
//
// Not during live traffic.
func NewRemoteClient(baseURL string, timeout time.Duration, namespace string, token string) (*RemoteClient, error) {
	const op = "cacheclient.new_remote_client"

	// Example:
	//
	//	baseURL = "https://cache.internal"
	//
	// Parse:
	//
	//	Scheme = https
	//	Host   = cache.internal
	//
	// Valid.
	//
	// --------------------------------
	//
	// Example:
	//
	//	baseURL = "cache.internal"
	//
	// Parse:
	//
	//	Scheme = ""
	//
	// Invalid.
	//
	// Why reject?
	//
	// Without scheme:
	//
	// We don't know:
	//
	//	http?
	//	https?
	//
	// Better to fail immediately.
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("%s: base url must include scheme and host", op)
	}

	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	// httpClient is reused for all cache requests.
	//
	// Why reuse it:
	//
	// http.Client is safe for concurrent use and reuses TCP connections,
	// making requests faster and reducing connection overhead.
	//
	// Timeout is the maximum total time allowed for one request.
	//
	// It includes:
	// - DNS lookup
	// - TCP connection
	// - TLS handshake
	// - sending the request
	// - waiting for response
	// - reading the response body
	//
	// Example:
	//
	//	Timeout: 2 * time.Second
	//
	// If the cache service does not respond within 2 seconds,
	// the request is automatically canceled.
	//
	// Why this matters:
	//
	// Prevents the gateway from waiting forever on a slow or broken cache service.
	return &RemoteClient{
		baseURL:    parsed,
		httpClient: &http.Client{Timeout: timeout},
		namespace:  strings.TrimSpace(namespace),
		token:      strings.TrimSpace(token),
	}, nil
}

// Get reads one value from the remote cache service.
//
// What it returns:
// - value: the cached bytes
// - error: ErrNotFound if the key does not exist, or a transport/server error
//
// Why this method exists:
// The gateway will later use this for config lookups and response caching.
// Get fetches a value from the remote cache service.
//
// Flow:
//
//  1. Validate and normalize the cache key.
//  2. Build an HTTP GET request.
//  3. Send request to cache service.
//  4. Read the response.
//  5. Decode the cached value.
//  6. Return the original bytes.
//
// Example:
//
//	Gateway
//	    |
//	    | GET /cache/user:123
//	    |
//	Cache Service
//	    |
//	    | {"value":"aGVsbG8="}
//	    |
//	    v
//
//	base64 decode
//
//	    |
//	    v
//
//	[]byte("hello")
//
// Why base64 is used:
//
// JSON cannot safely store raw binary bytes.
// The cache service sends bytes as a base64 string,
// then the client converts it back into the original bytes.
//
// Return cases:
//
//	200 -> value found
//	404 -> ErrNotFound
//	500 -> cache service error
func (c *RemoteClient) Get(ctx context.Context, key string) ([]byte, error) {
	key, err := normalizeKey(key)
	if err != nil {
		return nil, err
	}

	// Creates the outbound HTTP request.
	//
	// Example:
	//
	//	cache.Get(ctx, "user:123")
	//
	// becomes:
	//
	//	GET https://cache.internal/cache/user:123
	//
	// Why context is attached:
	//
	// If the client disconnects,
	// request timeout expires,
	// server shuts down,
	//
	// the cache request is automatically cancelled.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpointForKey(key), nil)
	if err != nil {
		return nil, err
	}

	c.applyHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var body getResponse
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return nil, err
		}

		if body.Value == "" {
			return nil, ErrNotFound
		}

		raw, err := base64.StdEncoding.DecodeString(body.Value)
		if err != nil {
			return nil, err
		}

		return raw, nil

	case http.StatusNotFound:
		return nil, ErrNotFound

	default:
		slurp, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("cache get failed: status=%d body=%s", resp.StatusCode, string(slurp))
	}
}

// Set writes one value into the remote cache service.
//
// What it does:
// - sends the value as base64
// - sends a TTL in seconds
//
// Why this method exists:
// Cache entries should expire cleanly and explicitly.
func (c *RemoteClient) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	key, err := normalizeKey(key)
	if err != nil {
		return err
	}
	if ttl <= 0 {
		return ErrInvalidTTL
	}

	body := setRequest{
		Value:      base64.StdEncoding.EncodeToString(value),
		TTLSeconds: int64(ttl.Seconds()),
	}
	if body.TTLSeconds <= 0 {
		body.TTLSeconds = 1
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.endpointForKey(key), bytes.NewReader(payload))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	c.applyHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return nil
	default:
		slurp, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("cache set failed: status=%d body=%s", resp.StatusCode, string(slurp))
	}
}

// Delete removes one key from the remote cache service.
//
// Why this method exists:
// Invalidation must be explicit. A gateway cache is only useful if it can be cleared.
func (c *RemoteClient) Delete(ctx context.Context, key string) error {
	key, err := normalizeKey(key)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.endpointForKey(key), nil)
	if err != nil {
		return err
	}

	c.applyHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	// Always close the response body.
	//
	// Why:
	//
	// HTTP connections are pooled and reused.
	//
	// If we forget:
	//
	//	resp.Body.Close()
	//
	// connections stay occupied and eventually
	// the application runs out of available connections.
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent:
		return nil
	case http.StatusNotFound:
		return ErrNotFound
	default:
		slurp, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("cache delete failed: status=%d body=%s", resp.StatusCode, string(slurp))
	}
}

// normalizeKey validates and trims a cache key.
//
// Why this exists:
// Cache keys should not contain hidden whitespace or empty values.
// Every cache operation starts by normalizing the key.
//
// Why:
//
// We never want:
//
//	"user:123"
//	" user:123 "
//	""
//
// to be treated inconsistently.
//
// Example:
//
//	" user:123 "
//
// becomes:
//
//	"user:123"
//
// Invalid keys fail immediately instead of making a bad network call.
func normalizeKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", ErrInvalidKey
	}
	return key, nil
}

// applyHeaders adds optional authorization headers.
//
// Why this exists:
// Some cache services require a token or secret header.
// Adds common cache service headers.
//
// Example:
//
//	Authorization: Bearer abc123
//	X-Namespace: gateway
//
// Why:
//
// Every cache request should carry authentication
// and namespace information automatically.
func (c *RemoteClient) applyHeaders(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

// endpointForKey builds the remote cache URL for one key.
//
// Why this exists:
// The gateway should talk to a stable endpoint format and keep key encoding safe.
// endpointForKey builds the final cache service URL for a specific cache key.
//
// Why this exists:
//
// The cache client should have one consistent way to build URLs.
// Callers should only provide a cache key and not worry about URL formatting.
//
// Example:
//
//	baseURL:
//	    https://cache.internal
//
//	namespace:
//	    gateway
//
//	key:
//	    user:123
//
// Generated key:
//
//	gateway:user:123
//
// After URL escaping:
//
//	gateway%3Auser%3A123
//
// Final URL:
//
//	https://cache.internal/v1/cache/gateway%3Auser%3A123
//
// Why namespace exists:
//
// Multiple applications may share the same cache service.
//
// Example:
//
//	gateway:user:123
//	auth:user:123
//	billing:user:123
//
// Even though the cache key is "user:123",
// each application gets its own isolated cache space.
//
// Why PathEscape is used:
//
// Cache keys may contain special characters:
//
//	user:123
//	user/email@test.com
//	tenant:acme/orders
//
// These characters can break URLs if not escaped.
//
// Example:
//
//	tenant:acme/orders
//
// becomes:
//
//	tenant%3Aacme%2Forders
//
// This guarantees the cache service receives the exact key.
func (c *RemoteClient) endpointForKey(key string) string {
	prefix := "/v1/cache"

	if c.namespace != "" {
		key = c.namespace + ":" + key
	}

	return joinURLPath(c.baseURL.String(), prefix, url.PathEscape(key))
}

// joinURLPath joins a base URL and path pieces safely.
//
// Why this exists:
// URL path joining should be explicit and predictable.
func joinURLPath(base string, parts ...string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")

	cleaned := make([]string, 0, len(parts)+1)
	cleaned = append(cleaned, base)

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		part = strings.Trim(part, "/")
		cleaned = append(cleaned, part)
	}

	final := strings.Join(cleaned, "/")
	if !strings.HasPrefix(final, "http://") && !strings.HasPrefix(final, "https://") {
		return path.Clean(final)
	}

	return final
}

// getResponse is the JSON shape returned by the remote cache service when reading a key.
//
// Why this exists:
// The client needs a stable response shape to decode values safely.
// getResponse represents the JSON returned by the cache service
// when reading a cached value.
//
// Example response from cache service:
//
//	{
//	    "value": "aGVsbG8="
//	}
//
// JSON decoder converts it into:
//
//	getResponse{
//	    Value: "aGVsbG8=",
//	}
//
// Why this struct exists:
//
// Decoding directly into maps is error-prone and less type-safe.
//
// Using a struct:
//
//   - documents the API contract
//   - provides compile-time safety
//   - makes JSON decoding simpler
//
// Flow:
//
//	Cache Service
//	       |
//	       v
//
//	{
//	    "value":"aGVsbG8="
//	}
//
//	       |
//	       v
//
//	getResponse.Value
//
//	       |
//	       v
//
//	base64 decode
//
//	       |
//	       v
//
//	[]byte("hello")
type getResponse struct {
	Value string `json:"value"`
}

// setRequest is the JSON shape sent to the remote cache service when writing a key.
//
// Why this exists:
// The cache service needs both the encoded value and the TTL.
// setRequest represents the JSON payload sent to the cache service
// when storing a value.
//
// Example:
//
// Key:
//
//	user:123
//
// Value:
//
//	[]byte("hello")
//
// TTL:
//
//	5 minutes
//
// Before sending:
//
//	setRequest{
//	    Value:      "aGVsbG8=",
//	    TTLSeconds: 300,
//	}
//
// JSON sent over HTTP:
//
//	{
//	    "value":"aGVsbG8=",
//	    "ttl_seconds":300
//	}
//
// Why Value is a string:
//
// JSON transports text more safely than raw binary data.
//
// Example:
//
//	[]byte("hello")
//
// becomes:
//
//	"aGVsbG8="
//
// using Base64 encoding.
//
// Why TTLSeconds exists:
//
// The cache service needs to know when the key should expire.
//
// Example:
//
//	TTLSeconds = 300
//
// Means:
//
//	Store this value for 5 minutes.
//
// After 5 minutes:
//
//	user:123
//
// is automatically removed from cache.
//
// Flow:
//
//	Gateway
//	    |
//	    | PUT
//	    |
//	    v
//
//	{
//	    "value":"aGVsbG8=",
//	    "ttl_seconds":300
//	}
//
//	    |
//	    v
//
//	Cache Service
//
//	    |
//	    v
//
//	Store key until expiration time.
type setRequest struct {
	Value      string `json:"value"`
	TTLSeconds int64  `json:"ttl_seconds"`
}
