package main

import (
	"io/ioutil"
	"net/http"
	"strings"
	"time"
)

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

// getClientInfo extracts client information
func getClientInfo(r *http.Request) []string {
	hostport := r.RemoteAddr
	host, port := "127.0.0.1", "0"

	// Basic parsing of host:port format
	if idx := strings.LastIndex(hostport, ":"); idx > 0 {
		host = hostport[:idx]
		port = hostport[idx+1:]
	}

	return []string{host, port}
}

// getServerInfo extracts server information
func getServerInfo(r *http.Request) []string {
	hostport := r.Host
	host, port := "localhost", "80"

	// Basic parsing of host:port format
	if idx := strings.LastIndex(hostport, ":"); idx > 0 {
		host = hostport[:idx]
		port = hostport[idx+1:]
	}

	return []string{host, port}
}
