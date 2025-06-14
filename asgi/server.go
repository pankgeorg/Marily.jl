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

	// Event queue management
	eventsMu      sync.Mutex
	events        []ASGIEvent
	eventsCond    = sync.NewCond(&eventsMu)
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

func init() {
	// Initialize the context for event management
	eventsContext, eventsCancel = context.WithCancel(context.Background())
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

	// Initialize the event queue
	eventsMu.Lock()
	events = make([]ASGIEvent, 0)
	eventsMu.Unlock()

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

	// Wake one waiting GetEvents call
	eventsMu.Lock()
	eventsCond.Signal()
	eventsMu.Unlock()

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
	// If context is already canceled, return immediately
	if eventsContext.Err() != nil {
		return C.CString("[]")
	}

	eventsMu.Lock()

	// If no events are available, wait for them
	if len(events) == 0 {
		// Create a channel to handle the timeout
		timeout := make(chan struct{})

		// Start a goroutine to handle timeout
		go func() {
			// Wait for either 30 seconds or until server stops
			select {
			// TODO: Figure out how much this timeout should be
			// Returns to julia after this
			case <-time.After(30 * time.Second):
				// Timeout occurred
			case <-eventsContext.Done():
				// Server is stopping
			}
			close(timeout)
			// Wake up the waiting goroutine
			eventsCond.Signal()
		}()

		// Wait for either events to arrive or timeout
		for len(events) == 0 {
			// Check if we should stop waiting (timeout or server shutdown)
			select {
			case <-timeout:
				eventsMu.Unlock()
				return C.CString("[]")
			default:
				// Continue waiting
			}

			// Wait for signal that new events are available
			eventsCond.Wait()

			// After waking up, check if the context was canceled
			if eventsContext.Err() != nil {
				eventsMu.Unlock()
				return C.CString("[]")
			}
		}
	}

	// We have events to return
	jsonData, err := json.Marshal(events)

	// Clear the events after fetching
	events = make([]ASGIEvent, 0)

	eventsMu.Unlock()

	if err != nil {
		return C.CString(fmt.Sprintf("Error marshaling events: %v", err))
	}

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

	// Store the event for Julia to process and signal waiting GetEvents calls
	eventsMu.Lock()
	events = append(events, event)
	eventsCond.Broadcast() // Signal that new events are available
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

func main() {
	// This is needed for building a shared library
}
