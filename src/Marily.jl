module Marily

export start_server, stop_server, register_event_handler, register_path_handler, run_server

# Load the shared object file
const libpath = joinpath(@__DIR__, "../asgi/libasgi.so")

# Global storage for the event handler callbacks
global event_handlers = Dict{String,Function}()
global event_callback_ptrs = Dict{String,Ptr{Cvoid}}()

# Thread safety for callback registration
const callback_lock = ReentrantLock()

# Define the C struct types to match asgi_structs.h exactly
struct AsgiString
    data::Ptr{Cchar}
    length::Csize_t
end

struct AsgiHeader
    name::AsgiString
    value::AsgiString
end

struct AsgiEvent
    request_id::AsgiString
    method::AsgiString
    path::AsgiString
    query_string::AsgiString
    scheme::AsgiString
    headers::Ptr{AsgiHeader}
    headers_count::Csize_t
    client::Ptr{AsgiString}
    server::Ptr{AsgiString}
    body::Ptr{Cuchar}
    body_length::Csize_t
    more_body::Cint
end

struct AsgiResponse
    request_id::AsgiString
    status::Cint
    headers::Ptr{AsgiHeader}
    headers_count::Csize_t
    body::Ptr{Cuchar}
    body_length::Csize_t
end

function __init__()
    # Ensure the library exists
    if !isfile(libpath)
        error("Cannot find ASGI Go library at $libpath")
    end
end

# Helper to create an AsgiString
function make_asgi_string(str::String)
    # Convert to array of bytes (UInt8) to ensure type compatibility
    bytes = Vector{UInt8}(str)

    # Allocate memory for the string data (+1 for null terminator)
    data = Base.Libc.malloc(length(bytes) + 1)

    # Convert to correct pointer type for copyto!
    data_ptr = convert(Ptr{UInt8}, data)

    # Copy the string data
    unsafe_copyto!(data_ptr, pointer(bytes), length(bytes))

    # Add null terminator
    unsafe_store!(data_ptr + length(bytes), 0x00)

    # Return the AsgiString structure
    return AsgiString(convert(Ptr{Cchar}, data), length(bytes))
end

# Helper to read an AsgiString to a Julia String
function read_asgi_string(str::AsgiString)
    if str.data == C_NULL || str.length == 0
        return ""
    end
    return unsafe_string(str.data, Int(str.length))
end

# Helper to create headers array
function make_asgi_headers(headers::Dict)
    count = 0
    for (_, values) in headers
        count += length(values)
    end

    if count == 0
        return (C_NULL, 0)
    end

    headers_ptr = Base.Libc.malloc(count * sizeof(AsgiHeader))
    idx = 0

    for (name, values) in headers
        for value in values
            header_ptr = convert(Ptr{Nothing}, headers_ptr + idx * sizeof(AsgiHeader))
            name_asgi = make_asgi_string(String(name))
            value_asgi = make_asgi_string(String(value))

            # Store the name
            unsafe_store!(Ptr{AsgiString}(header_ptr), name_asgi)

            # Store the value
            value_offset = fieldoffset(AsgiHeader, 2)
            unsafe_store!(Ptr{AsgiString}(header_ptr + value_offset), value_asgi)

            idx += 1
        end
    end

    return (convert(Ptr{AsgiHeader}, headers_ptr), Csize_t(count))
end

# Helper to create an AsgiResponse
function make_asgi_response(request_id::String, status::Int, headers::Dict, body::Vector{UInt8})
    # Create the response struct
    response_ptr = Base.Libc.malloc(sizeof(AsgiResponse))

    # Set request_id
    req_id_asgi = make_asgi_string(request_id)
    unsafe_store!(Ptr{AsgiString}(response_ptr), req_id_asgi)

    # Set status
    status_offset = fieldoffset(AsgiResponse, 2)
    unsafe_store!(Ptr{Cint}(response_ptr + status_offset), Cint(status))

    # Set headers
    headers_ptr, headers_count = make_asgi_headers(headers)
    headers_ptr_offset = fieldoffset(AsgiResponse, 3)
    headers_count_offset = fieldoffset(AsgiResponse, 4)
    unsafe_store!(Ptr{Ptr{AsgiHeader}}(response_ptr + headers_ptr_offset), headers_ptr)
    unsafe_store!(Ptr{Csize_t}(response_ptr + headers_count_offset), headers_count)

    # Set body
    body_ptr_offset = fieldoffset(AsgiResponse, 5)
    body_length_offset = fieldoffset(AsgiResponse, 6)

    if !isempty(body)
        body_ptr = Base.Libc.malloc(length(body))
        # Convert to correct pointer type for copyto!
        data_ptr = convert(Ptr{UInt8}, body_ptr)
        unsafe_copyto!(data_ptr, pointer(body), length(body))
        unsafe_store!(Ptr{Ptr{Cuchar}}(response_ptr + body_ptr_offset), convert(Ptr{Cuchar}, body_ptr))
        unsafe_store!(Ptr{Csize_t}(response_ptr + body_length_offset), Csize_t(length(body)))
    else
        unsafe_store!(Ptr{Ptr{Cuchar}}(response_ptr + body_ptr_offset), C_NULL)
        unsafe_store!(Ptr{Csize_t}(response_ptr + body_length_offset), Csize_t(0))
    end

    return convert(Ptr{AsgiResponse}, response_ptr)
end

"""
    process_event_callback(event_ptr::Ptr{AsgiEvent})::Ptr{AsgiResponse}

C-callable function that processes an ASGI event received from Go.
This is the function that Go will call directly when an event occurs.
"""
function process_event_callback(handler)
    return function (event_ptr::Ptr{AsgiEvent})
        try
            # Load the event struct
            event = unsafe_load(event_ptr)

            # Extract path to determine which handler to use
            path = read_asgi_string(event.path)

            # Find the appropriate handler for this path

            # Check if we have a handler registered
            if handler === nothing
                @warn "No handler found for path: $path"
                return C_NULL
            end

            # Extract fields from the event
            request_id = read_asgi_string(event.request_id)
            method = read_asgi_string(event.method)
            query_string = read_asgi_string(event.query_string)
            scheme = read_asgi_string(event.scheme)

            # Extract headers
            headers = Dict{String,Vector{String}}()
            for i in 0:(event.headers_count-1)
                header_ptr = event.headers + i * sizeof(AsgiHeader)
                header = unsafe_load(convert(Ptr{AsgiHeader}, header_ptr))
                name = read_asgi_string(header.name)
                value = read_asgi_string(header.value)

                if !haskey(headers, name)
                    headers[name] = String[]
                end
                push!(headers[name], value)
            end

            # Extract client and server info
            client = ["unknown", "0"]
            if event.client != C_NULL
                client_host = read_asgi_string(unsafe_load(convert(Ptr{AsgiString}, event.client)))
                client_port = read_asgi_string(unsafe_load(convert(Ptr{AsgiString}, event.client + sizeof(AsgiString))))
                client = [client_host, client_port]
            end

            server = ["localhost", "80"]
            if event.server != C_NULL
                server_host = read_asgi_string(unsafe_load(convert(Ptr{AsgiString}, event.server)))
                server_port = read_asgi_string(unsafe_load(convert(Ptr{AsgiString}, event.server + sizeof(AsgiString))))
                server = [server_host, server_port]
            end

            # Extract body
            body = UInt8[]
            if event.body != C_NULL && event.body_length > 0
                body = unsafe_wrap(Array, convert(Ptr{UInt8}, event.body), Int(event.body_length), own=false)
                # Make a copy since we don't own the memory
                body = copy(body)
            end

            # Build scope
            scope = Dict(
                "type" => "http",
                "http_version" => "1.1",
                "method" => method,
                "scheme" => scheme,
                "path" => path,
                "query_string" => query_string,
                "headers" => headers,
                "client" => client,
                "server" => server
            )

            # Build message
            message = Dict(
                "body" => body,
                "more_body" => event.more_body !== Cint(0)
            )

            # Build event object
            event_obj = Dict(
                "type" => "http.request",
                "request_id" => request_id,
                "scope" => scope,
                "message" => message
            )

            # Call the handler
            response = handler(event_obj)

            # If no response is needed or handler returned nothing
            if response === nothing
                return C_NULL
            end

            # Extract response components
            status, headers, body = response

            # Convert body to vector of bytes if it's a string
            if body isa String
                body = Vector{UInt8}(body)
            end

            # Create and return the response
            return make_asgi_response(request_id, status, headers, body)

        catch e
            # Handle any errors in the callback
            @error "Error in Julia callback" exception = (e, catch_backtrace())
            return C_NULL
        finally
            ccall((:freeAsgiEvent, libpath), Cvoid, (Ptr{AsgiEvent},), event_ptr)
        end
    end
end

"""
    register_path_handler(path::String, handler::Function)

Register a callback function for a specific path.
The handler should accept an event and return a tuple of (status, headers, body) or nothing.

Path can end with /* to match all paths with that prefix.
"""
function register_path_handler(path::String, handler)
    c_handler = @cfunction($handler, Ptr{AsgiResponse}, (Ptr{AsgiEvent},))
    # Register the callback with Go for this path
    path_cstr = Base.unsafe_convert(Cstring, Base.cconvert(Cstring, path))
    result = ccall((:RegisterEventCallback, libpath), Cstring,
        (Cstring, Ptr{Cvoid}),
        path_cstr, c_handler)

    message = unsafe_string(result)
    Libc.free(result)
    @info message
    return message
end

"""
    register_event_handler(handler::Function)

Register a callback function for the root path (/).
This is a convenience function that calls register_path_handler("/", handler).
"""
function register_event_handler(handler::Function)
    return register_path_handler("/", handler)
end

"""
    start_server(port::Int)

Start the ASGI HTTP server on the specified port.
"""
function start_server(port::Int)
    result = ccall((:StartServer, libpath), Cstring, (Cint,), port)
    message = unsafe_string(result)
    Libc.free(result)
    return message
end

"""
    stop_server()

Stop the running ASGI HTTP server.
"""
function stop_server()
    result = ccall((:StopServer, libpath), Cstring, ())
    message = unsafe_string(result)
    Libc.free(result)
    return message
end

"""
    run_server(port::Int, handler::Function)

Run the server with the provided event handler.
This is a simplified interface that registers the handler and starts the server.
"""
function run_server(port::Int, handler::Function)
    # Register the handler
    register_message = register_event_handler(handler)
    @info "Registered event handler: $register_message"

    # Start the server
    start_message = start_server(port)
    @info "Started server: $start_message"

    try
        # Keep the Julia process running
        while true
            sleep(1)
        end
    catch e
        if isa(e, InterruptException)
            @info "Server interrupted, shutting down..."
        else
            @error "Error in server loop" exception = (e, catch_backtrace())
        end
    finally
        stop_server()
    end
end

end # module
