module Marily

using JSON3


export start_server, stop_server, get_events, process_events, submit_response, run_server, run_server_threaded

# Load the shared object file
const libpath = joinpath(@__DIR__, "../asgi/libasgi.so")

function __init__()
    # Ensure the library exists
    if !isfile(libpath)
        error("Cannot find ASGI Go library at $libpath")
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
    get_events()

Retrieve all ASGI events from the server and clear the event queue.
Returns a Vector of event dictionaries.
"""
function get_events()
    result = ccall((:GetEvents, libpath), Cstring, ())
    json_str = unsafe_string(result)
    Libc.free(result)

    if json_str == "[]"
        return []
    end

    # Parse JSON string into Julia objects
    return JSON3.read(json_str)
end

"""
    submit_response(request_id::String, status::Int, headers::Dict, body::Vector{UInt8})

Submit a response for a specific request back to the Go server.
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
    process_events(handler::Function)

Process all pending ASGI events with the provided handler function.
The handler should accept an event and return a tuple of (status, headers, body)
or nothing if no response is needed.
"""
function process_events(handler::Function)
    events = get_events()
    if isempty(events)
        return 0
    end

    processed = 0
    for event in events
        # Handle each event
        response = handler(event)

        # If the handler returned a response, submit it
        if response !== nothing
            request_id = event.request_id
            status, headers, body = response
            submit_response(request_id, status, headers, body)
            processed += 1
        end
    end

    return processed
end

"""
    process_events_async(handler::Function)

Process events asynchronously in a separate task.
Returns a task handle that can be waited on if needed.
"""
function process_events_async(handler::Function)
    return @async process_events(handler)
end

"""
    process_event(event, handler::Function)

Process a single event with the provided handler function.
"""
function process_event(event, handler::Function)
    response = handler(event)
    if response !== nothing
        request_id = event.request_id
        status, headers, body = response
        submit_response(request_id, status, headers, body)
        return 1
    end
    return 0
end

"""
    run_server(port::Int, handler::Function; polling_interval::Float64=0.0)

Run the server and process events in a loop until interrupted.
Single-threaded version.
"""
function run_server(port::Int, handler::Function; polling_interval::Float64=0.0)
    start_server(port)
    try
        while true
            process_events(handler)
            polling_interval > 0 && sleep(polling_interval)
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

"""
    run_server_threaded(port::Int, handler::Function;
                      polling_interval::Float64=0.0,
                      num_threads::Int=Threads.nthreads())

Run the server and distribute event processing across all available Julia threads.
Efficiently handles high request volumes by parallelizing event processing.
"""
function run_server_threaded(port::Int, handler::Function;
    polling_interval::Float64=0.0,
    num_threads::Int=Threads.nthreads())
    # Check if Julia was started with multiple threads
    if Threads.nthreads() <= 1
        @warn "Julia is running with only one thread. For better performance, start Julia with multiple threads using: julia --threads=auto"
        return run_server(port, handler; polling_interval=polling_interval)
    end

    # Limit number of worker threads to available threads (minus one for the main thread)
    actual_threads = min(num_threads, Threads.nthreads() - 1)
    actual_threads = max(1, actual_threads)  # Ensure at least one worker thread

    # Create a channel for tracking active workers
    active_workers = Threads.Atomic{Int}(0)

    # Start worker threads
    worker_tasks = Task[]
    for i in 1:actual_threads
        task = Threads.@spawn begin
            # Mark this worker as active
            Threads.atomic_add!(active_workers, 1)
            try
                while true
                    events = get_events()
                    # Get an event from the channel or wait
                    for event in events
                        # Process the event
                        try
                            process_event(event, handler)
                        catch e
                            @error "Error processing event" exception = (e, catch_backtrace()) event = event
                        end
                    end
                end
            finally
                # Mark this worker as inactive
                Threads.atomic_sub!(active_workers, 1)
            end
        end
        push!(worker_tasks, task)
    end

    # Start the server
    start_message = start_server(port)
    @info "Server started" message = start_message threads = actual_threads
    try
        wait()
    catch e
        if isa(e, InterruptException)
            @info "Server interrupted, shutting down..."
        else
            @error "Error in server loop" exception = (e, catch_backtrace())
        end
    finally

        # Wait for all workers to finish processing their current events
        @info "Waiting for worker threads to finish..."
        timeout = 10.0  # seconds
        start_time = time()
        while Threads.atomic_get(active_workers) > 0
            if time() - start_time > timeout
                @warn "Timeout waiting for worker threads to finish"
                break
            end
            sleep(0.1)
        end

        # Stop the server
        stop_server()
    end
end

end # module
