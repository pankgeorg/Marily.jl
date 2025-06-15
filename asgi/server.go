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

// Global variables to manage the server state
var (
	server   *http.Server
	serverMu sync.Mutex

	// Event queue management using a channel-based approach for better performance
	eventsCh      = make(chan ASGIEvent, 10000) // Buffered channel for better performance
	eventsContext context.Context
	eventsCancel  context.CancelFunc

	// Pending responses mechanism
	pendingMu    sync.Mutex
	pendingReqs        = make(map[string]chan ASGIResponse)
	requestIdSeq int64 = 0
)

// ASGIEvent represents a standard ASGI event
type ASGIEvent struct {
	Type      string                 `json:"type"`
	RequestId string                 `json:"request_id"`
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

func init() {
	// Initialize the context for event management
	eventsContext, eventsCancel = context.WithCancel(context.Background())

	// Create a buffered event channel
	eventsCh = make(chan ASGIEvent, 10000)
}

//export StartServer
func StartServer(port int) *C.char {
	serverMu.Lock()
	defer serverMu.Unlock()

	if server != nil {
		return C.CString("Server is already running")
	}

	// Create a fresh context for this server instance
	if eventsCancel != nil {
		eventsCancel()
	}
	eventsContext, eventsCancel = context.WithCancel(context.Background())

	// Create a fresh event channel
	eventsCh = make(chan ASGIEvent, 10000)

	// Reset pending requests
	pendingMu.Lock()
	pendingReqs = make(map[string]chan ASGIResponse)
	pendingMu.Unlock()

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

	return C.CString(fmt.Sprintf("Server started on port %d", port))
}

//export StopServer
func StopServer() *C.char {
	serverMu.Lock()
	defer serverMu.Unlock()

	if server == nil {
		return C.CString("Server is not running")
	}

	// Signal any blocked GetEvents calls to return
	if eventsCancel != nil {
		eventsCancel()
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

	server = nil
	return C.CString("Server stopped")
}

//export GetEvents
func GetEvents() *C.char {
	// Fast path: check if context is already canceled
	if eventsContext.Err() != nil {
		return C.CString("[]")
	}

	// Wait for an event with timeout
	select {
	case event := <-eventsCh:
		// We got an event
		result := []ASGIEvent{event}

		// Try to get more events without blocking (up to 10)
		for i := 0; i < 9; i++ {
			select {
			case ev := <-eventsCh:
				result = append(result, ev)
			default:
				// No more events available without blocking
				break
			}
		}

		// Marshal the events
		jsonData, err := json.Marshal(result)
		if err != nil {
			return C.CString(fmt.Sprintf("Error marshaling events: %v", err))
		}
		return C.CString(string(jsonData))

	case <-time.After(5 * time.Second):
		// Timeout - return empty array
		return C.CString("[]")

	case <-eventsContext.Done():
		// Server is stopping
		return C.CString("[]")
	}
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

	// Send the event to the channel for Julia to process
	select {
	case eventsCh <- event:
		// Event successfully queued
	default:
		// Channel is full, handle overflow (this prevents blocking)
		fmt.Printf("Warning: Event channel is full, dropping oldest event\n")
		// Remove oldest event and try again
		select {
		case <-eventsCh: // Remove oldest
			eventsCh <- event // Try again
		default:
			// This should not happen but handles race conditions
		}
	}

	// Wait for Julia to process the event and provide a response
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

	case <-time.After(30 * time.Second):
		// Timeout after waiting too long
		pendingMu.Lock()
		delete(pendingReqs, requestId)
		close(respChan)
		pendingMu.Unlock()

		w.WriteHeader(http.StatusGatewayTimeout)
		w.Write([]byte("Request timed out waiting for processing"))
	}
}

// Functions from events.go would be used here...
// generateRequestId(), createASGIEvent(), etc.

func main() {
	// This is needed for building a shared library
}
