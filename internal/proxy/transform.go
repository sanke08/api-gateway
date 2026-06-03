package proxy

import (
	"net/http"
	"strings"

	"github.com/sanke08/api_gateway/internal/models"
)

// applyRequestTransform modifies the outgoing request before it reaches the upstream.
//
// What it does:
// - removes unwanted headers
// - adds trusted gateway headers
// - optionally rewrites the path
//
// Why this exists:
// This gives the gateway a controlled place to shape requests without touching
// backend services.
//
// Important:
// We only modify headers and paths here. We do not modify request bodies in this phase.
func applyRequestTransform(r *http.Request, transform models.RequestTransform) {
	removeHeaders(r.Header, transform.RemoveHeaders)

	for key, value := range transform.AddHeaders {
		if key == "" {
			continue
		}
		r.Header.Set(key, value)
	}

	if transform.RewritePath != "" {
		r.URL.Path = cleanPath(transform.RewritePath)
	}
}

// applyResponseTransform modifies the upstream response before it goes back to the client.
//
// What it does:
// - removes unwanted upstream headers
// - adds gateway-controlled response headers
//
// Why this exists:
// The gateway should be able to present a clean, controlled response surface
// to the client without exposing internal upstream details.
func applyResponseTransform(resp *http.Response, transform models.ResponseTransform) {
	if resp == nil {
		return
	}

	removeHeaders(resp.Header, transform.RemoveHeaders)

	for key, value := range transform.AddHeaders {
		if key == "" {
			continue
		}
		resp.Header.Set(key, value)
	}
}

// removeHeaders removes the given header names from a header map.
//
// Why this exists:
// The same removal logic is used for both request and response transforms.
func removeHeaders(h http.Header, names []string) {
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		h.Del(name)
	}
}
