using Marily: Marily, process_event_callback

# Define a handler function
function my_handler(event)
    # Create a response
    status = 200
    headers = Dict("Content-Type" => ["text/plain"])
    y = 4 # + sum(rand(100, 400) * rand(400, 100))
    body = "Hello from Julia! You requested: $(event["scope"]["path"]) $(y) \n"
    return (status, headers, body)
end

my_handler1 = process_event_callback(my_handler)
my_handler2 = process_event_callback(function (event)
    return (200, Dict("Content-Type" => ["text/plain"]), "Another path response")
end)
# Start the server and process events
Marily.register_path_handler("POST /another_path", my_handler2)

Marily.register_path_handler("localhost:8000/path2", my_handler2)

Marily.run_server(8000, my_handler1)
