using Marily: Marily, process_event_callback, AsgiEvent

# Define a handler function
function my_handler(event)
    # Create a response
    status = 200
    headers = Dict("Content-Type" => ["text/plain"])
    y = 4 + sum(rand(100, 40) * rand(40, 100))
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

Marily.register_path_handler("/another_path", my_handler2)
Marily.register_path_handler("POST /another", my_handler3)
Marily.register_path_handler("localhost/path2", my_handler2)

# Start the server and process events
println("pid: $(getpid())")
Marily.run_server(8000, my_handler1)
