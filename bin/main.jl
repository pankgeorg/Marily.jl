using Marily

# Define a handler function
function my_handler(event)
    # Create a response
    status = 200
    headers = Dict("Content-Type" => ["text/plain"])
    y = sum(rand(1000, 40) * rand(40, 1000))
    body = "Hello from Julia! You requested: $(event.scope.path) $(y) \n"
    return (status, headers, body)
end

# Start the server and process events
Marily.run_server_threaded(8000, my_handler)
