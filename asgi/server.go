package main

// #include <stdlib.h>
// typedef char* (*EventCallbackFn)(const char*);
//
// // C helper function that does the casting for us
// static inline char* callEventCallback(void* fn_ptr, char* input) {
//   if (fn_ptr == NULL) return NULL;
//   EventCallbackFn fn = (EventCallbackFn)fn_ptr;
//   return fn(input);
// }
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// Configuration constants
const (
	// Maximum number of concurrent requests to process
	maxConcurrentRequests = 1000
	// Request timeout for callback in seconds
	callbackTimeout = 30
)

// Global variables to manage the server state
var (
	server   *http.Server
	serverMu sync.Mutex

	// Event callback mechanism
	eventCallbackMu sync.Mutex
	eventCallback   unsafe.Pointer // Stores the Julia callback function

	// Request ID generation
	requestIdSeq int64 = 0

	// Semaphore to limit concurrent requests
	// Using a buffered channel as a counting semaphore
	requestSemaphore = make(chan struct{}, maxConcurrentRequests)
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

//export RegisterEventCallback
func RegisterEventCallback(callback unsafe.Pointer) *C.char {
	eventCallbackMu.Lock()
	defer eventCallbackMu.Unlock()

	eventCallback = callback
	return C.CString("Event callback registered successfully")
}

//export StartServer
func StartServer(port int) *C.char {
	serverMu.Lock()
	defer serverMu.Unlock()

	if server != nil {
		return C.CString("Server is already running")
	}

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

//export GetConcurrentRequests
func GetConcurrentRequests() *C.char {
	// Count how many slots are available in the semaphore
	available := 0
	for i := 0; i < maxConcurrentRequests; i++ {
		select {
		case requestSemaphore <- struct{}{}:
			available++
		default:
			break
		}
	}

	// Return the tokens we just took
	for i := 0; i < available; i++ {
		<-requestSemaphore
	}

	inUse := maxConcurrentRequests - available
	return C.CString(fmt.Sprintf("%d/%d concurrent requests active", inUse, maxConcurrentRequests))
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
	case <-time.After(5 * time.Second):
		// Could not get a token within timeout, server is overloaded
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("Server is at capacity, please try again later"))
		return
	}

	// Get the callback
	callback := eventCallback
	// Check if we have a callback registered
	if callback == nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("No event handler registered"))
		return
	}

	// Generate a unique request ID
	requestId := generateRequestId()

	// Create an ASGI event from the HTTP request with the request ID
	event := createASGIEvent(r, requestId)

	// Convert event to JSON
	eventJSON, err := json.Marshal(event)
	if err != nil {
		fmt.Printf("Error marshaling event: %v\n", err)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Error preparing request"))
		return
	}

	// Call the Julia callback directly with the event
	cEventJSON := C.CString(string(eventJSON))
	defer C.free(unsafe.Pointer(cEventJSON))

	// Set up a timeout for the callback
	var cResponseJSON *C.char
	responseChan := make(chan *C.char, 1)
	timeoutChan := time.After(time.Duration(callbackTimeout) * time.Second)

	// Call the callback in a goroutine to allow timeout
	go func() {
		result := C.callEventCallback(callback, cEventJSON)
		responseChan <- result
	}()

	// Wait for the callback to complete or timeout
	select {
	case cResponseJSON = <-responseChan:
		// Callback completed
	case <-timeoutChan:
		// Callback timed out
		close(responseChan)
		w.WriteHeader(http.StatusGatewayTimeout)
		w.Write([]byte("Request processing timed out"))
		return
	}

	// Check if we got a valid response
	if cResponseJSON == nil {
		close(responseChan)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("No response from event handler"))
		return
	}

	// Convert to Go string and free the memory
	responseJSON := C.GoString(cResponseJSON)
	C.free(unsafe.Pointer(cResponseJSON)) // Free the memory allocated by Julia

	// Check if the response is empty
	if responseJSON == "" {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Empty response from event handler"))
		return
	}

	// Parse the response
	var response ASGIResponse
	if err := json.Unmarshal([]byte(responseJSON), &response); err != nil {
		fmt.Printf("Error unmarshaling callback response: %v\nResponse content: %q\n", err, responseJSON)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Error processing response"))
		return
	}
	// Write the response to the client
	writeResponse(w, response)
}

// Helper function to write the response to the HTTP writer
func writeResponse(w http.ResponseWriter, response ASGIResponse) {
	// Write the response headers
	for key, values := range response.Headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Write the status code and body
	w.WriteHeader(response.Status)
	w.Write(response.Body)
}

// generateRequestId creates a unique ID for each request
func generateRequestId() string {
	id := atomic.AddInt64(&requestIdSeq, 1)
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), id)
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
