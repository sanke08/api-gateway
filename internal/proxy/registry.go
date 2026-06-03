package proxy

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/sanke08/api_gateway/internal/models"
)

// ErrMissingTarget means the requested tenant does not have
// any configured upstream backend.
//
// Example:
//
// Tenant resolved:
//
//	tenantID = "acme"
//
// Registry lookup:
//
//	target, ok := registry.Get("acme")
//
// If no upstream exists:
//
//	ok == false
//
// the proxy can return:
//
//	404 Tenant Not Configured
//
// or
//
//	502 Bad Gateway
//
// depending on gateway policy.
//
// Why use a shared error:
//
// A stable sentinel error allows callers to detect
// this specific failure using:
//
//	errors.Is(err, ErrMissingTarget)
//
// instead of string matching.
var ErrMissingTarget = errors.New("upstream target not configured")

// StaticRegistry stores upstream targets entirely in memory.
//
// Think of it as:
//
// TenantID -> UpstreamTarget
//
// Example:
//
//	acme     -> https://api.acme.internal
//	amazon   -> https://api.amazon.internal
//	flipkart -> https://api.flipkart.internal
//
// Why this exists:
//
// During early phases of the gateway,
// loading from memory is simpler than:
//
// - PostgreSQL
// - MySQL
// - Redis
// - Service Discovery
//
// Request flow:
//
//	Request
//	   |
//	   v
//	Tenant Resolver
//	   |
//	   v
//	tenantID = "acme"
//	   |
//	   v
//	Registry Lookup
//	   |
//	   v
//	https://api.acme.internal
//
// Later this same interface could be backed by:
//
// - database
// - config service
// - consul
// - etcd
// - kubernetes CRDs
type StaticRegistry struct {
	// targets is the primary lookup table.
	//
	// Structure:
	//
	//	map[TenantID]UpstreamTarget
	//
	// Example:
	//
	//	{
	//	    "acme": {
	//	        TenantID: "acme",
	//	        BaseURL: "https://api.acme.internal",
	//	    },
	//	    "amazon": {
	//	        TenantID: "amazon",
	//	        BaseURL: "https://api.amazon.internal",
	//	    },
	//	}
	//
	// Why a map is used:
	//
	// Lookup becomes:
	//
	//	targets["acme"]
	//
	// Complexity:
	//
	//	O(1)
	//
	// instead of:
	//
	//	O(n)
	//
	// scanning a slice every request.
	//
	// Since every request needs tenant lookup,
	// fast access is critical.
	targets map[string]models.UpstreamTarget

	// order stores tenant IDs in deterministic order.
	//
	// Example:
	//
	//	["acme", "amazon", "flipkart"]
	//
	// Why this exists:
	//
	// Go maps do NOT guarantee iteration order.
	//
	// Example:
	//
	//	for k := range targets
	//
	// could produce:
	//
	//	amazon
	//	acme
	//	flipkart
	//
	// on one run,
	//
	// and:
	//
	//	flipkart
	//	acme
	//	amazon
	//
	// on another.
	//
	// For:
	//
	// - tests
	// - logs
	// - startup compilation
	// - debugging
	//
	// deterministic ordering is useful.
	//
	// Therefore tenant IDs are collected
	// and sorted once.
	order []string
}

// NewStaticRegistry validates configuration
// and builds the in-memory registry.
//
// Startup validation is extremely important.
//
// Bad:
//
// Application starts successfully
// and crashes during traffic.
//
// Good:
//
// Application refuses to start.
//
// Example invalid configuration:
//
//	{
//	    TenantID: "",
//	    BaseURL: "https://api.acme.internal",
//	}
//
// Startup immediately fails.
//
// This is called:
//
// "fail fast"
//
// and is a common production practice.

// Registry provides upstream targets to the proxy layer.
//
// Why this interface exists:
// It keeps the proxy layer independent from where targets come from.
// Today it can be in-memory. Later it can come from the database.
func NewStaticRegistry(targets []models.UpstreamTarget) (*StaticRegistry, error) {
	const op = "proxy.new_static_registry"

	out := &StaticRegistry{
		targets: make(map[string]models.UpstreamTarget),
		order:   make([]string, 0, len(targets)),
	}

	for _, target := range targets {
		target.TenantID = strings.TrimSpace(target.TenantID)
		target.Name = strings.TrimSpace(target.Name)
		target.BaseURL = strings.TrimSpace(target.BaseURL)
		// Normalize path prefixes.
		//
		// Example:
		//
		//	"gateway/"
		//
		// becomes:
		//
		//	"/gateway"
		//
		// Example:
		//
		//	"v1/"
		//
		// becomes:
		//
		//	"/v1"
		//
		// Why:
		//
		// Path rewriting later should not depend
		// on inconsistent configuration formats.
		target.StripPrefix = cleanPath(target.StripPrefix)
		target.AddPrefix = cleanPath(target.AddPrefix)

		if target.TenantID == "" {
			return nil, fmt.Errorf("%s: tenant_id is required", op)
		}
		if target.BaseURL == "" {
			return nil, fmt.Errorf("%s: base_url is required for tenant %s", op, target.TenantID)
		}
		if _, exists := out.targets[target.TenantID]; exists {
			return nil, fmt.Errorf("%s: duplicate target for tenant %s", op, target.TenantID)
		}

		out.targets[target.TenantID] = target
		out.order = append(out.order, target.TenantID)
	}

	sort.Strings(out.order)

	return out, nil
}

// All returns all upstream targets in stable order.
//
// Why this exists:
// The proxy handler compiles all upstream proxies once at startup.
func (r *StaticRegistry) All() []models.UpstreamTarget {
	if r == nil {
		return nil
	}

	out := make([]models.UpstreamTarget, 0, len(r.order))
	for _, tenantID := range r.order {
		out = append(out, r.targets[tenantID])
	}
	return out
}

// Get returns one upstream target by tenant ID.
//
// Why this exists:
// It is useful for tests and future admin tools.
func (r *StaticRegistry) Get(tenantID string) (models.UpstreamTarget, bool) {
	if r == nil {
		return models.UpstreamTarget{}, false
	}

	target, ok := r.targets[strings.TrimSpace(tenantID)]
	return target, ok
}
