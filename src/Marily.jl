module Marily

using JSON3

export start_server, stop_server, submit_response, register_event_handler, run_server

# Load the shared object file
const libpath = joinpath(@__DIR__, "../asgi/libasgi.so")

# Global storage for the event handler callback
global event_handler_func = nothing
global event_callback_ptr = C_NULL

# Thread safety for callback registration
const callback_lock = ReentrantLock()

function __init__()
    # Ensure the library exists
    if !isfile(libpath)
        error("Cannot find ASGI Go library at $libpath")
    end
end

"""
    process_event_callback(event_json::Cstring)::Cstring

C-callable function that processes an ASGI event received from Go.
This is the function that Go will call directly when an event occurs.
"""
function process_event_callback(event_json::Cstring)::Cstring
    try
        # Parse the event JSON
        json_str = unsafe_string(event_json)
        event = JSON3.read(json_str)

        # Check if we have a handler registered
        if event_handler_func === nothing
            return C_NULL
        end

        # Call the handler
        response = event_handler_func(event)

        # If no response is needed or handler returned nothing
        if response === nothing
            return C_NULL
        end

        # Extract response components
        request_id = event.request_id
        status, headers, body = response

        # Create response object
        resp_obj = Dict(
            "request_id" => request_id,
            "status" => status,
            "headers" => headers,
            "body" => body isa String ? Vector{UInt8}(body) : body
        )

        # Convert to JSON
        json_response = JSON3.write(resp_obj)

        # Return a C string with the response
        # Note: This memory will be freed by Go
        return Base.unsafe_convert(Cstring, Base.cconvert(Cstring, json_response))
    catch e
        # Handle any errors in the callback
        error_msg = "Error in Julia callback: $(sprint(showerror, e, catch_backtrace()))"
        @error error_msg

        # Return an error response
        # The memory will be freed by Go
        return Base.unsafe_convert(Cstring, Base.cconvert(Cstring, error_msg))
    end
end

"""
    register_event_handler(handler::Function)

Register a callback function that will be directly called by Go when an ASGI event is received.
The handler should accept an event and return a tuple of (status, headers, body) or nothing.
"""
function register_event_handler(handler::Function)
    lock(callback_lock) do
        global event_handler_func = handler

        # Create a C-callable function that will invoke our handler
        c_handler = @cfunction(process_event_callback, Cstring, (Cstring,))
        global event_callback_ptr = c_handler

        # Register the callback with Go
        result = ccall((:RegisterEventCallback, libpath), Cstring, (Ptr{Cvoid},), c_handler)
        message = unsafe_string(result)
        Libc.free(result)

        return message
    end
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
    submit_response(request_id::String, status::Int, headers::Dict, body::Vector{UInt8})

Submit a response for a specific request back to the Go server.
Note: This is kept for compatibility but may not be needed with the callback approach.
"""
function submit_response(request_id::String, status::Int, headers::Dict, body::Vector{UInt8})
    # Create the response object
    response = Dict(
        "request_id" => request_id,
        "status" => status,
        "headers" => headers,
        "body" => body
    )

    # Convert to JSON
    json_response = JSON3.write(response)

    # Call the Go function
    result = ccall((:SubmitResponse, libpath), Cstring, (Cstring,), json_response)
    message = unsafe_string(result)
    Libc.free(result)

    return message
end

"""
    submit_response(request_id::String, status::Int, headers::Dict, body::String)

Convenience method that takes a string body instead of raw bytes.
"""
function submit_response(request_id::String, status::Int, headers::Dict, body::String)
    submit_response(request_id, status, headers, Vector{UInt8}(body))
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
