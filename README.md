# ASGI Go-Julia Bridge

This project provides a Go-based HTTP server that generates ASGI events, 
which can be consumed by Julia applications through a shared library (.so) interface.

## History

I've been struggling with figuring out a way to run julia code in a web server.
`HTTP.jl` has been a huge effort, but my current belief is that julia just shouldn't
be doing that much IO in any case. I've experimented with Nginx Unit, but it's C interface
is unstable and it also requires running a daemon, configuring the server and managing the
lifecycle of the server. 

Then I figured out that golang can compile to a static library, and that I can use that
to call julia code from a go-based web server. Go's `net/http` isn't `go`ing anywhere
and it's a very stable fast and well-tested library. Julia can call that static library,
so the only question is how to pass the events from the web server to the julia code. 
The solution would be to use a shared memory buffer, but for the time being I'm using just
some json strings around. Should be good for most of work, and works at about 15.000 rps
on my machine.

Needless to say, this is a very hacky solution and it's mostly written by AI (thanks Claude).
It's not production ready, but it's a good starting point for a more robust solution and for us
to have a discussion.

## Building the Go Library

To build the shared library:

```bash
cd asgi
go build -buildmode=c-shared -o libasgi.so .
```

## Using the Julia Wrapper

See `main/bin.jl`.

```bash
julia --project=. bin/main.jl
```

## Requirements

- Go 1.18+ for building the shared library
- Julia 1.6+ for running the wrapper
