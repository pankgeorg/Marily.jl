# ASGI Go-Julia Bridge

This project provides a Go-based HTTP server that generates ASGI events, 
which can be consumed by Julia applications through a shared library (.so) interface.

## History

I've been struggling with figuring out a way to run julia code in a web server.
`HTTP.jl` has been a huge effort, but my current belief is that julia just shouldn't
be doing that much IO in any case. I've experimented with Nginx Unit, but its C interface
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

## Process Checkpointing with CriuJulia

This project includes `CriuJulia.jl`, a Julia interface to CRIU (Checkpoint/Restore In Userspace)
that allows checkpointing and restoring Julia processes. This can be useful for:

- Saving application state and restoring it later
- Reducing startup time by restoring from a checkpoint
- Creating snapshots of long-running processes

### Important Requirements

⚠️ **Julia must be started with IO_URING disabled**:
```bash
UV_USE_IO_URING=0 julia --project=.
```

Without this setting, checkpointing will likely fail due to incompatibilities between CRIU and Julia's (libuv)
IO subsystem.

### Example Usage
```bash
# Start Julia with IO_URING disabled
UV_USE_IO_URING=0 julia --project=. bin/side.jl
```

In your Julia code:

```julia
using CriuJulia

# Create a checkpoint in the specified directory
self_checkpoint("/path/to/checkpoint", false)

# Checkpoint with custom options
self_checkpoint(
    "/path/to/checkpoint",    # Directory to store checkpoint files
    true,                     # Keep process running after checkpoint
    "/path/to/log.txt",       # Log file
    4,                        # Log level (higher = more verbose)
    "/var/run/criu_service.socket"  # CRIU service socket path
)
```

### Warning

⚠️ **This functionality is experimental and not production-ready**:
- CRIU checkpointing is highly sensitive to system configurations
- Restoring processes with external resources (files, sockets, etc.) may be problematic
- Some Julia features may not be properly saved/restored
- Requires a running CRIU service with appropriate permissions
- Different Julia versions may have different compatibility with CRIU

Use for development and experimentation only.

## Building criu

Building libcriu.so has many requirements. To make the library, do

```bash
git clone https://github.com/checkpoint-restore/criu
cd criu
make
```

Then make julia aware of the `criu/lib/c/libcriu.so` object.

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
- CRIU installed for process checkpointing
