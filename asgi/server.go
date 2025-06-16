package main

// #include <stdlib.h>
// #include <string.h>
// #include "asgi_structs.h"
//
// // Helper function to create an asgi_string
// static inline asgi_string make_asgi_string(const char* str) {
//     asgi_string result;
//     if (str == NULL) {
//         result.data = NULL;
//         result.length = 0;
//     } else {
//         size_t len = strlen(str);
//         result.data = (char*)malloc(len + 1);
//         strcpy(result.data, str);
//         result.length = len;
//     }
//     return result;
// }
//
// // Helper to free an asgi_string
// static inline void free_asgi_string(asgi_string str) {
//     if (str.data != NULL) {
//         free(str.data);
//     }
// }
//
// // Helper to free an asgi_event
// static inline void free_asgi_event(asgi_event* event) {
//     if (event == NULL) return;
//
//     free_asgi_string(event->request_id);
//     free_asgi_string(event->method);
//     free_asgi_string(event->path);
//     free_asgi_string(event->query_string);
//     free_asgi_string(event->scheme);
//
//     // Free headers
//     for (size_t i = 0; i < event->headers_count; i++) {
//         free_asgi_string(event->headers[i].name);
//         free_asgi_string(event->headers[i].value);
//     }
//     if (event->headers != NULL) {
//         free(event->headers);
//     }
//
//     // Free client
//     if (event->client != NULL) {
//         // Access client array elements by pointer arithmetic
//         free_asgi_string(*(asgi_string*)((char*)event->client));
//         free_asgi_string(*(asgi_string*)((char*)event->client + sizeof(asgi_string)));
//         free(event->client);
//     }
//
//     // Free server
//     if (event->server != NULL) {
//         // Access server array elements by pointer arithmetic
//         free_asgi_string(*(asgi_string*)((char*)event->server));
//         free_asgi_string(*(asgi_string*)((char*)event->server + sizeof(asgi_string)));
//         free(event->server);
//     }
//
//     // Free body
//     if (event->body != NULL) {
//         free(event->body);
//     }
//
//     // Free the event itself
//     free(event);
// }
//
// // Helper to free an asgi_response
// static inline void free_asgi_response(asgi_response* response) {
//     if (response == NULL) return;
//
//     free_asgi_string(response->request_id);
//
//     // Free headers
//     for (size_t i = 0; i < response->headers_count; i++) {
//         free_asgi_string(response->headers[i].name);
//         free_asgi_string(response->headers[i].value);
//     }
//     if (response->headers != NULL) {
//         free(response->headers);
//     }
//
//     // Free body
//     if (response->body != NULL) {
//         free(response->body);
//     }
//
//     // Free the response itself
//     free(response);
// }
//
// // C helper function that calls the callback safely
// static inline asgi_response* call_event_callback(asgi_callback_fn callback, asgi_event* event) {
//     if (callback == NULL) return NULL;
//     return callback(event);
// }
import "C"

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
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

	// Global router/multiplexer
	globalMux = http.NewServeMux()

	// Path-to-callback mapping
	callbacksMu sync.RWMutex
	callbacks   = make(map[string]C.asgi_callback_fn)

	// Request ID generation
	requestIdSeq int64 = 0

	// Semaphore to limit concurrent requests
	// Using a buffered channel as a counting semaphore
	requestSemaphore = make(chan struct{}, maxConcurrentRequests)
)

// Convert a Go string to a C asgi_string
func goStringToAsgiString(s string) C.asgi_string {
	return C.make_asgi_string(C.CString(s))
}

// Convert HTTP headers to C asgi_header array
func headersToAsgiHeaders(headers http.Header) (*C.asgi_header, C.size_t) {
	count := 0
	for _, values := range headers {
		count += len(values)
	}

	// Add one for the Host header if not present
	if _, ok := headers["Host"]; !ok {
		count++
	}

	if count == 0 {
		return nil, 0
	}

	// Allocate memory for headers
	asgiHeaders := (*C.asgi_header)(C.malloc(C.size_t(count) * C.size_t(unsafe.Sizeof(C.asgi_header{}))))
	idx := 0

	// Convert each header
	for name, values := range headers {
		for _, value := range values {
			// Convert to lowercase as per ASGI spec
			headerName := strings.ToLower(name)
			header := (*C.asgi_header)(unsafe.Pointer(uintptr(unsafe.Pointer(asgiHeaders)) +
				uintptr(idx)*unsafe.Sizeof(C.asgi_header{})))

			header.name = goStringToAsgiString(headerName)
			header.value = goStringToAsgiString(value)
			idx++
		}
	}

	// Add Host header if not present
	if _, ok := headers["Host"]; !ok {
		header := (*C.asgi_header)(unsafe.Pointer(uintptr(unsafe.Pointer(asgiHeaders)) +
			uintptr(idx)*unsafe.Sizeof(C.asgi_header{})))
		header.name = goStringToAsgiString("host")
		header.value = goStringToAsgiString("localhost")
	}

	return asgiHeaders, C.size_t(count)
}

// createAsgiEvent converts an HTTP request to a C asgi_event
func createAsgiEvent(r *http.Request, requestId string) *C.asgi_event {
	// Allocate memory for the event
	event := (*C.asgi_event)(C.malloc(C.size_t(unsafe.Sizeof(C.asgi_event{}))))

	// Set request ID
	event.request_id = goStringToAsgiString(requestId)

	// Set method
	event.method = goStringToAsgiString(r.Method)

	// Set path
	event.path = goStringToAsgiString(r.URL.Path)

	// Set query string
	event.query_string = goStringToAsgiString(r.URL.RawQuery)

	// Set scheme
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	event.scheme = goStringToAsgiString(scheme)

	// Set headers
	event.headers, event.headers_count = headersToAsgiHeaders(r.Header)

	// Set client info - Fix: can't use array indexing with *C.asgi_string
	clientInfo := (*C.asgi_string)(C.malloc(2 * C.size_t(unsafe.Sizeof(C.asgi_string{}))))
	hostStr, portStr := "127.0.0.1", "0"
	if idx := strings.LastIndex(r.RemoteAddr, ":"); idx > 0 {
		hostStr = r.RemoteAddr[:idx]
		portStr = r.RemoteAddr[idx+1:]
	}

	// Fix: Set client array elements using pointer arithmetic
	hostClientPtr := (*C.asgi_string)(unsafe.Pointer(uintptr(unsafe.Pointer(clientInfo))))
	portClientPtr := (*C.asgi_string)(unsafe.Pointer(uintptr(unsafe.Pointer(clientInfo)) +
		unsafe.Sizeof(C.asgi_string{})))

	*hostClientPtr = goStringToAsgiString(hostStr)
	*portClientPtr = goStringToAsgiString(portStr)
	event.client = clientInfo

	// Set server info - Fix: can't use array indexing with *C.asgi_string
	serverInfo := (*C.asgi_string)(C.malloc(2 * C.size_t(unsafe.Sizeof(C.asgi_string{}))))
	hostStr, portStr = "localhost", "80"
	if idx := strings.LastIndex(r.Host, ":"); idx > 0 {
		hostStr = r.Host[:idx]
		portStr = r.Host[idx+1:]
	}

	// Fix: Set server array elements using pointer arithmetic
	hostServerPtr := (*C.asgi_string)(unsafe.Pointer(uintptr(unsafe.Pointer(serverInfo))))
	portServerPtr := (*C.asgi_string)(unsafe.Pointer(uintptr(unsafe.Pointer(serverInfo)) +
		unsafe.Sizeof(C.asgi_string{})))

	*hostServerPtr = goStringToAsgiString(hostStr)
	*portServerPtr = goStringToAsgiString(portStr)
	event.server = serverInfo

	// Set body
	if r.Body != nil {
		bodyBytes, _ := ioutil.ReadAll(r.Body)
		r.Body.Close()

		if len(bodyBytes) > 0 {
			bodyPtr := C.malloc(C.size_t(len(bodyBytes)))
			C.memcpy(bodyPtr, unsafe.Pointer(&bodyBytes[0]), C.size_t(len(bodyBytes)))
			event.body = (*C.uchar)(bodyPtr)
			event.body_length = C.size_t(len(bodyBytes))
		} else {
			event.body = nil
			event.body_length = 0
		}
	} else {
		event.body = nil
		event.body_length = 0
	}

	// Set more_body
	event.more_body = C.bool(false)

	return event
}

// writeResponseFromC writes an ASGI response to the HTTP response writer
func writeResponseFromC(w http.ResponseWriter, response *C.asgi_response) {
	// Set headers
	for i := 0; i < int(response.headers_count); i++ {
		header := (*C.asgi_header)(unsafe.Pointer(uintptr(unsafe.Pointer(response.headers)) +
			uintptr(i)*unsafe.Sizeof(C.asgi_header{})))

		name := C.GoStringN(header.name.data, C.int(header.name.length))
		value := C.GoStringN(header.value.data, C.int(header.value.length))
		w.Header().Add(name, value)
	}

	// Set status code
	w.WriteHeader(int(response.status))

	// Write body
	if response.body != nil && response.body_length > 0 {
		body := C.GoBytes(unsafe.Pointer(response.body), C.int(response.body_length))
		w.Write(body)
	}
}

//export RegisterEventCallback
func RegisterEventCallback(path *C.char, callback C.asgi_callback_fn) *C.char {
	pathStr := C.GoString(path)

	// Add handler to the global mux if needed
	globalMux.HandleFunc(pathStr, handleRequestWithCallback(callback))
	fmt.Print("Event callback registered for path: ", pathStr, "\n")
	return C.CString(fmt.Sprintf("Event callback registered for path: %s", pathStr))
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

	// Create a new server using the global mux
	server = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: globalMux,
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

// handleRequestWithCallback processes incoming HTTP requests and creates ASGI events
func handleRequestWithCallback(callback C.asgi_callback_fn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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

		// Check if we have a callback registered
		if callback == nil {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("No handler registered for this path"))
			return
		}

		// Generate a unique request ID
		requestId := generateRequestId()

		// Create a C asgi_event from the HTTP request
		cEvent := createAsgiEvent(r, requestId)
		defer C.free_asgi_event(cEvent)

		// Set up a timeout for the callback
		var cResponse *C.asgi_response
		responseChan := make(chan *C.asgi_response, 1)
		timeoutChan := time.After(time.Duration(callbackTimeout) * time.Second)

		// Call the callback in a goroutine to allow timeout
		go func() {
			result := C.call_event_callback(callback, cEvent)
			responseChan <- result
		}()

		// Wait for the callback to complete or timeout
		select {
		case cResponse = <-responseChan:
			// Callback completed
		case <-timeoutChan:
			// Callback timed out
			w.WriteHeader(http.StatusGatewayTimeout)
			w.Write([]byte("Request processing timed out"))
			return
		}

		// Check if we got a valid response
		if cResponse == nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("No response from event handler"))
			return
		}

		// Write the response to the client and free it
		writeResponseFromC(w, cResponse)
		C.free_asgi_response(cResponse)
	}
}

// generateRequestId creates a unique ID for each request
func generateRequestId() string {
	id := atomic.AddInt64(&requestIdSeq, 1)
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), id)
}

func main() {
	// This is needed for building a shared library
}
