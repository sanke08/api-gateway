package observability

import (
	"maps"
	"sort"
	"strings"
)

// Labels is a simple key/value set used to tag metrics.
//
// Why this exists:
// A metric without labels is too coarse for a multi-tenant gateway.
//
// Example labels:
//
//	tenant=tenant-123
//	route=/products
//	method=GET
//	status=200
//
// Why it is a map:
// Maps are easy to construct at the call site and easy to read in code.
// Labels is a collection of metric tags.
//
// Think of labels as dimensions that help us break down one metric
// into smaller groups.
//
// Example:
//
//	http_requests_total
//
// without labels:
//
//	http_requests_total = 1,000,000
//
// This tells us total requests,
// but we do not know:
//
// - which tenant generated them
// - which route received them
// - which HTTP method was used
// - which status code was returned
//
// Labels solve that problem.
//
// Example:
//
//	Labels{
//	    "tenant": "tenant-123",
//	    "route":  "/products",
//	    "method": "GET",
//	    "status": "200",
//	}
//
// Result:
//
//	http_requests_total{
//	    tenant="tenant-123",
//	    route="/products",
//	    method="GET",
//	    status="200",
//	}
//
// Why this is a map:
//
// Labels are naturally key/value pairs.
//
//	tenant -> tenant-123
//	method -> GET
//	status -> 200
//
// Using a map makes creation simple:
//
//	labels := Labels{
//	    "tenant": "tenant-123",
//	    "method": "GET",
//	}
type Labels map[string]string

// Clone returns a deep copy of the label set.
//
// Why this matters:
// Labels may be reused by callers. The registry should keep its own copy so
// later changes in the caller do not alter stored metrics.
// Clone creates a completely separate copy of the label set.
//
// Why this matters:
//
// Maps in Go are reference types.
//
// Example:
//
//	original := Labels{
//	    "tenant": "tenant-123",
//	}
//
//	copied := original
//
//	copied["tenant"] = "tenant-999"
//
// Now:
//
//	original["tenant"]
//
// is ALSO:
//
//	tenant-999
//
// because both variables point to the same map.
//
// Clone prevents that problem.
//
// Example:
//
//	original := Labels{
//	    "tenant": "tenant-123",
//	}
//
//	copied := original.Clone()
//
//	copied["tenant"] = "tenant-999"
//
// Result:
//
//	original -> tenant-123
//	copied   -> tenant-999
//
// The metric registry should always store its own copy,
// otherwise callers could accidentally modify already-recorded metrics.
func (l Labels) Clone() Labels {
	if l == nil {
		return Labels{}
	}

	out := make(Labels, len(l))
	maps.Copy(out, l)
	return out
}

// String returns a stable label string with keys sorted alphabetically.
//
// Why this exists:
// Stable ordering makes metric output deterministic and easier to test.
//
// Example:
//
//	Labels{"tenant":"a", "method":"GET"} -> method="GET",tenant="a"
//
// String converts labels into a deterministic text format.
//
// Example:
//
//	Labels{
//	    "tenant": "a",
//	    "method": "GET",
//	}
//
// becomes:
//
//	method="GET",tenant="a"
//
// Why convert labels to a string?
//
// Many metric systems need a stable identifier.
//
// Example:
//
//	http_requests_total{
//	    method="GET",
//	    tenant="a",
//	}
//
// The label string can become part of the metric key.
//
// Why sort keys?
//
// Maps do NOT guarantee iteration order.
//
// Example:
//
// First execution:
//
//	tenant="a",method="GET"
//
// Second execution:
//
//	method="GET",tenant="a"
//
// Same labels,
// different output.
//
// That creates unstable metric keys.
//
// Sorting fixes that.
//
// Every execution becomes:
//
//	method="GET",tenant="a"
//
// which is deterministic and easy to test.
func (l Labels) String() string {
	if len(l) == 0 {
		return ""
	}

	// Collect all label names.
	//
	// Example:
	//
	// Labels{
	//     "tenant":"a",
	//     "method":"GET",
	// }
	//
	// becomes:
	//
	// ["tenant","method"]
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}

	// Sort alphabetically.
	//
	// Before:
	//
	// ["tenant","method"]
	//
	// After:
	//
	// ["method","tenant"]
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))

	for _, k := range keys {
		// Escape quotes inside label values.
		//
		// Example:
		//
		// value:
		//
		// hello "world"
		//
		// becomes:
		//
		// hello \"world\"
		//
		// This keeps output valid.
		v := strings.ReplaceAll(l[k], `"`, `\"`)

		// Build:
		//
		// method="GET"
		//
		// tenant="a"
		parts = append(parts, k+`="`+v+`"`)
	}

	// Join all labels into one stable string.
	//
	// Example:
	//
	// [
	//     method="GET",
	//     tenant="a",
	// ]
	//
	// becomes:
	//
	// method="GET",tenant="a"
	return strings.Join(parts, ",")
}
