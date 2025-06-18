using Marily: Marily, process_event_callback, AsgiEvent

# Define a handler function
function my_handler(event)
    # Create a response
    status = 200
    headers = Dict("Content-Type" => ["text/plain"])
    y = 4 + sum(rand(100, 400) * rand(400, 100))
    body = "Hello from Julia! You requested: $(event["scope"]["path"]) $(y) \n"
    return (status, headers, body)
end

my_handler1 = process_event_callback(my_handler)
my_handler2 = process_event_callback(function (event)
    return (200, Dict("Content-Type" => ["text/plain"]), "Another path response $(event["scope"]["path"])")
end)

my_handler3 = process_event_callback(function (event)
    return (200, Dict("Content-Type" => ["text/plain"]), "Another path response 3 $(event["scope"]["path"])")
end)

# Warning! If the function is not precompiled,
# calling it _twice_ from go will lead to either a segmentation
# fault and/or a deadlock, as the julia runtime will be 
# precompiling, but go will be hammering that function pointer
# nonetheless
precompile(process_event_callback, (Function,))
precompile(my_handler1, (Ptr{AsgiEvent},))
precompile(my_handler2, (Ptr{AsgiEvent},))
precompile(my_handler3, (Ptr{AsgiEvent},))

Marily.register_path_handler("/another_path", my_handler2)
Marily.register_path_handler("POST /another", my_handler3)
Marily.register_path_handler("localhost/path2", my_handler2)

# Start the server and process events
Marily.run_server(8000, my_handler1)
