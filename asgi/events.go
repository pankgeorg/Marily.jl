package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"sync/atomic"
	"time"
)

// generateRequestId creates a unique ID for each request
func generateRequestId() string {
	id := atomic.AddInt64(&requestIdSeq, 1)
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), id)
}

// createASGIEvent converts an HTTP request to an ASGI event with a request ID
func createASGIEvent(r *http.Request, requestId string) ASGIEvent {
	// Read the body content
	bodyBytes, _ := ioutil.ReadAll(r.Body)
	defer r.Body.Close()

	// Create the ASGI scope
	scope := map[string]interface{}{
		"type":         "http",
		"asgi":         map[string]interface{}{"version": "3.0", "spec_version": "2.1"},
		"http_version": r.Proto,
		"method":       r.Method,
		"scheme":       getScheme(r),
		"path":         r.URL.Path,
		"query_string": r.URL.RawQuery,
		"root_path":    "",
		"headers":      getHeadersList(r),
		"client":       getClientInfo(r),
		"server":       getServerInfo(r),
	}

	// Create the ASGI message
	message := map[string]interface{}{
		"body":      bodyBytes,
		"more_body": false,
	}

	return ASGIEvent{
		Type:      "http.request",
		RequestId: requestId,
		Scope:     scope,
		Message:   message,
		Time:      time.Now(),
	}
}

// getScheme determines if the request was HTTP or HTTPS
func getScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// Helper function to access request headers that was referenced in the original code
func getHeaders(r *http.Request) map[string][]string {
	headers := make(map[string][]string)
	for name, values := range r.Header {
		headers[name] = values
	}
	return headers
}

// getClientInfo extracts client information
func getClientInfo(r *http.Request) []string {
	host, port := splitHostPort(r.RemoteAddr)
	return []string{host, port}
}

// getServerInfo extracts server information
func getServerInfo(r *http.Request) []string {
	// A simplified version - in production, you'd parse this properly
	host, port := "localhost", "80"
	return []string{host, port}
}

// splitHostPort is a helper to split host:port strings
func splitHostPort(hostport string) (string, string) {
	// Simple implementation - in a real app, you'd use net.SplitHostPort
	// and handle edge cases more robustly
	host, port := "localhost", "80"
	// Implementation omitted for brevity
	return host, port
}
