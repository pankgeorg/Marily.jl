using Marily

# Define a handler function
function my_handler(event)
    # Create a response
    status = 200
    headers = Dict("Content-Type" => ["text/plain"])
    y = 4 #  sum(rand(100, 400) * rand(400, 100))
    body = "Hello from Julia! You requested: $(event.scope.path) $(y) \n"
    return (status, headers, body)
end

# Start the server and process events
Marily.run_server(8000, my_handler)
