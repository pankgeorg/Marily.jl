package main

import (
	"fmt"
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
	// Read the body content (simplified for example)
	// TODO:  In a real implementation, you'd want to handle larger bodies appropriately

	// Alternative (TODO)
	// bodyBytes, _ := ioutil.ReadAll(r.Body)
	// defer r.Body.Close()

	body := []byte{}
	if r.Body != nil {
		// Read up to 1MB of request body
		body = make([]byte, 1<<20)
		n, _ := r.Body.Read(body)
		if n > 0 {
			body = body[:n]
		}
		r.Body.Close()
	}

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
		"body":      body,
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

// getHeadersList converts HTTP headers to ASGI format (list of [key, value] pairs)
func getHeadersList(r *http.Request) [][]string {
	headers := make([][]string, 0)
	for name, values := range r.Header {
		for _, value := range values {
			headers = append(headers, []string{name, value})
		}
	}
	// Add host header
	headers = append(headers, []string{"host", r.Host})
	return headers
}
