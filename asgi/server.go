package main

import (
	"C"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Configuration constants
const (
	// Maximum number of concurrent requests to process
	maxConcurrentRequests = 1000
	// Request timeout in seconds
	requestTimeout = 30
)

// Global variables to manage the server state
var (
	server   *http.Server
	eventsMu sync.Mutex
	events   []ASGIEvent
	serverMu sync.Mutex

	// Pending responses mechanism
	pendingMu    sync.Mutex
	pendingReqs        = make(map[string]chan ASGIResponse)
	requestIdSeq int64 = 0

	// Semaphore to limit concurrent requests
	// Using a buffered channel as a counting semaphore
	requestSemaphore = make(chan struct{}, maxConcurrentRequests)
)

// ASGIEvent represents a standard ASGI event
type ASGIEvent struct {
	Type      string                 `json:"type"`
	RequestId string                 `json:"request_id"` // Added request ID for tracking
	Scope     map[string]interface{} `json:"scope"`
	Message   map[string]interface{} `json:"message"`
	Time      time.Time              `json:"time"`
}

// ASGIResponse holds the response data from Julia
type ASGIResponse struct {
	RequestId string              `json:"request_id"`
	Status    int                 `json:"status"`
	Headers   map[string][]string `json:"headers"`
	Body      []byte              `json:"body"`
}

//export StartServer
func StartServer(port int) *C.char {
	serverMu.Lock()
	defer serverMu.Unlock()

	if server != nil {
		return C.CString("Server is already running")
	}

	// Initialize the event queue
	eventsMu.Lock()
	events = make([]ASGIEvent, 0)
	eventsMu.Unlock()

	// Reset pending requests
	pendingMu.Lock()
	pendingReqs = make(map[string]chan ASGIResponse)
	pendingMu.Unlock()

	// Reset the semaphore
	requestSemaphore = make(chan struct{}, maxConcurrentRequests)

	// Create a new server
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRequest)

	server = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	// Start the server in a goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("HTTP server error: %v\n", err)
		}
	}()

	return C.CString(fmt.Sprintf("Server started on port %d with max %d concurrent requests", port, maxConcurrentRequests))
}

//export StopServer
func StopServer() *C.char {
	serverMu.Lock()
	defer serverMu.Unlock()

	if server == nil {
		return C.CString("Server is not running")
	}

	// Create a context with a timeout for graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		return C.CString(fmt.Sprintf("Error shutting down server: %v", err))
	}

	// Clean up pending requests
	pendingMu.Lock()
	for id, ch := range pendingReqs {
		close(ch)
		delete(pendingReqs, id)
	}
	pendingMu.Unlock()

	// Drain the semaphore to unblock any waiting goroutines
	for i := 0; i < maxConcurrentRequests; i++ {
		select {
		case requestSemaphore <- struct{}{}:
			// Added a token
		default:
			// Semaphore is full
			break
		}
	}

	server = nil
	return C.CString("Server stopped")
}

//export GetEvents
func GetEvents() *C.char {
	eventsMu.Lock()
	defer eventsMu.Unlock()

	if len(events) == 0 {
		return C.CString("[]")
	}

	jsonData, err := json.Marshal(events)
	if err != nil {
		return C.CString(fmt.Sprintf("Error marshaling events: %v", err))
	}

	// Clear the events after fetching
	events = make([]ASGIEvent, 0)

	return C.CString(string(jsonData))
}

//export SubmitResponse
func SubmitResponse(responseJson *C.char) *C.char {
	respStr := C.GoString(responseJson)

	var response ASGIResponse
	if err := json.Unmarshal([]byte(respStr), &response); err != nil {
		return C.CString(fmt.Sprintf("Error unmarshaling response: %v", err))
	}

	// Find the corresponding request channel
	pendingMu.Lock()
	respChan, exists := pendingReqs[response.RequestId]
	pendingMu.Unlock()

	if !exists {
		return C.CString(fmt.Sprintf("No pending request with ID: %s", response.RequestId))
	}

	// Send the response to the waiting goroutine
	respChan <- response

	return C.CString("Response submitted successfully")
}

// handleRequest processes incoming HTTP requests and creates ASGI events
func handleRequest(w http.ResponseWriter, r *http.Request) {
	// Try to acquire a semaphore token with a short timeout
	// This prevents the server from accepting more requests than it can handle
	select {
	case requestSemaphore <- struct{}{}:
		// Got a token, proceed with the request
		defer func() {
			// Always release the token when done
			<-requestSemaphore
		}()
	case <-time.After(30 * time.Second):
		// Could not get a token within timeout, server is overloaded
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("Server is at capacity, please try again later"))
		return
	}

	// Generate a unique request ID
	requestId := generateRequestId()

	// Create a channel for this request's response
	respChan := make(chan ASGIResponse, 1)

	// Register this channel in our pending requests map
	pendingMu.Lock()
	pendingReqs[requestId] = respChan
	pendingMu.Unlock()

	// Create an ASGI event from the HTTP request with the request ID
	event := createASGIEvent(r, requestId)

	// Store the event for Julia to process
	eventsMu.Lock()
	events = append(events, event)
	eventsMu.Unlock()

	// Wait for Julia to process the event and provide a response
	// Include a timeout to prevent hanging forever
	select {
	case response := <-respChan:
		// Clean up the pending request
		pendingMu.Lock()
		delete(pendingReqs, requestId)
		pendingMu.Unlock()

		// Write the response headers
		for key, values := range response.Headers {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}

		// Write the status code and body
		w.WriteHeader(response.Status)
		w.Write(response.Body)

	case <-time.After(time.Duration(requestTimeout) * time.Second):
		// Timeout after waiting too long
		pendingMu.Lock()
		delete(pendingReqs, requestId)
		close(respChan)
		pendingMu.Unlock()

		w.WriteHeader(http.StatusGatewayTimeout)
		w.Write([]byte("Request timed out waiting for processing"))
	}
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

func main() {
	// This is needed for building a shared library
}
