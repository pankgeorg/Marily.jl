#ifndef ASGI_STRUCTS_H
#define ASGI_STRUCTS_H

#include <stdlib.h>
#include <stdbool.h>

// String representation
typedef struct {
    char* data;
    size_t length;
} asgi_string;

// Header pair
typedef struct {
    asgi_string name;
    asgi_string value;
} asgi_header;

// ASGI event
typedef struct {
    asgi_string request_id;
    asgi_string method;
    asgi_string path;
    asgi_string query_string;
    asgi_string scheme;
    asgi_header* headers;
    size_t headers_count;
    asgi_string* client;
    asgi_string* server;
    unsigned char* body;
    size_t body_length;
    bool more_body;
} asgi_event;

// ASGI response
typedef struct {
    asgi_string request_id;
    int status;
    asgi_header* headers;
    size_t headers_count;
    unsigned char* body;
    size_t body_length;
} asgi_response;

// Callback function type
typedef asgi_response* (*asgi_callback_fn)(asgi_event*);

#endif // ASGI_STRUCTS_H