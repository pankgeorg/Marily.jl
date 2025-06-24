module CriuJulia

export self_checkpoint, criu_init_opts, criu_set_service_address, criu_set_images_dir_fd,
    criu_set_pid, criu_set_leave_running, criu_set_ext_unix_sk, criu_set_tcp_established,
    criu_set_evasive_devices, criu_set_shell_job, criu_set_file_locks, criu_set_log_level,
    criu_set_log_file, criu_check, criu_dump, criu_restore, criu_restore_child

# Path to the libcriu shared library
const libcriu = "/home/pgeorgakopoulos/criu/lib/c/libcriu.so"

"""
    self_checkpoint(dir=mktempdir(), leave_running=false; log_file=nothing, log_level=2)

Checkpoint the current Julia process using libcriu.

# Arguments
- `dir`: Directory where checkpoint images will be stored (default: temporary directory)
- `leave_running`: Whether to keep the process running after checkpointing (default: false)
- `log_file`: Path to log file (default: none)
- `log_level`: Log verbosity level (default: 2)
- `criu_address`: Address of the CRIU service (default: "/var/run/criu_service.socket")

# Returns
- `0` on success, negative value on error

# Example
```julia
self_checkpoint("/path/to/checkpoint", true)
```
"""
function self_checkpoint(dir=mktempdir(), leave_running=false, log_file=nothing, log_level=2, criu_address="/var/run/criu_service.socket")
    # Create the directory if it doesn't exist
    isdir(dir) || mkdir(dir)
    # Initialize CRIU options
    ret = criu_init_opts()
    if ret != 0
        error("Failed to initialize CRIU options: $ret")
    end

    # Set the service address (use default)
    criu_set_service_address(criu_address)

    # Open the directory for images and get file descriptor
    dir_fd = -1
    GC.@preserve dir begin
        dir_path = Base.unsafe_convert(Cstring, dir)
        dir_fd = ccall(:open, Cint, (Cstring, Cint), dir_path, 0x00010000) # O_DIRECTORY = 0x00010000
        if dir_fd == -1
            error("Failed to open directory: $dir")
        end
    end

    try
        # Set required options
        criu_set_images_dir_fd(dir_fd)
        criu_set_pid(getpid())
        criu_set_leave_running(leave_running)

        # Set optional parameters
        criu_set_tcp_established(true)
        criu_set_shell_job(true)
        criu_set_file_locks(true)

        # Set logging options
        criu_set_log_level(log_level)
        if log_file !== nothing
            GC.@preserve log_file begin
                criu_set_log_file(log_file)
            end
        end

        # Perform the checkpoint
        println("Checkpointing Julia process $(getpid()) to directory: $dir")
        ret = criu_dump()

        if ret == 0
            println("Checkpoint successful")
        else
            println("Checkpoint failed with error code: $ret")
        end

        return ret
    finally
        # Always close the directory file descriptor
        if dir_fd != -1
            ccall(:close, Cint, (Cint,), dir_fd)
        end
    end
end

# Thread-safe libcriu bindings

"""
    criu_init_opts()

Initialize CRIU request options. Must be called before using any other functions from libcriu,
except criu_check().

Returns 0 on success and -1 on failure.
"""
function criu_init_opts()
    ccall((:criu_init_opts, libcriu), Cint, ())
end

"""
    criu_set_service_address(address)

Specify path to a CRIU service socket. Pass NULL to use the default address.
"""
function criu_set_service_address(address::Union{String,Nothing})
    addr_ptr = address === nothing ? C_NULL : pointer(address)
    ccall((:criu_set_service_address, libcriu), Cvoid, (Ptr{Cchar},), addr_ptr)
end

"""
    criu_set_images_dir_fd(fd)

Set file descriptor for the directory where images will be stored/read from.
This option is required for dump and restore operations.
"""
function criu_set_images_dir_fd(fd::Integer)
    ccall((:criu_set_images_dir_fd, libcriu), Cvoid, (Cint,), fd)
end

"""
    criu_set_pid(pid)

Set PID of the process to dump. If not set, CRIU will dump the calling process.
"""
function criu_set_pid(pid::Integer)
    ccall((:criu_set_pid, libcriu), Cvoid, (Cint,), pid)
end

"""
    criu_set_leave_running(leave_running)

Set whether to leave the process running after checkpoint.
"""
function criu_set_leave_running(leave_running::Bool)
    ccall((:criu_set_leave_running, libcriu), Cvoid, (Cuchar,), leave_running)
end

"""
    criu_set_ext_unix_sk(ext_unix_sk)

Set whether to dump external unix sockets.
"""
function criu_set_ext_unix_sk(ext_unix_sk::Bool)
    ccall((:criu_set_ext_unix_sk, libcriu), Cvoid, (Cuchar,), ext_unix_sk)
end

"""
    criu_set_tcp_established(tcp_established)

Set whether to dump established TCP connections.
"""
function criu_set_tcp_established(tcp_established::Bool)
    ccall((:criu_set_tcp_established, libcriu), Cvoid, (Cuchar,), tcp_established)
end

"""
    criu_set_evasive_devices(evasive_devices)

Set whether to use evasive devices.
"""
function criu_set_evasive_devices(evasive_devices::Bool)
    ccall((:criu_set_evasive_devices, libcriu), Cvoid, (Cuchar,), evasive_devices)
end

"""
    criu_set_shell_job(shell_job)

Set whether the process is a shell job.
"""
function criu_set_shell_job(shell_job::Bool)
    ccall((:criu_set_shell_job, libcriu), Cvoid, (Cuchar,), shell_job)
end

"""
    criu_set_file_locks(file_locks)

Set whether to dump file locks.
"""
function criu_set_file_locks(file_locks::Bool)
    ccall((:criu_set_file_locks, libcriu), Cvoid, (Cuchar,), file_locks)
end

"""
    criu_set_log_level(log_level)

Set log level for CRIU.
"""
function criu_set_log_level(log_level::Integer)
    ccall((:criu_set_log_level, libcriu), Cvoid, (Cint,), log_level)
end

"""
    criu_set_log_file(log_file)

Set log file for CRIU.
"""
function criu_set_log_file(log_file::String)
    ccall((:criu_set_log_file, libcriu), Cvoid, (Ptr{Cchar},), pointer(log_file))
end

"""
    criu_check()

Check if CRIU is available.
Returns 0 on success or negative error code on failure.
"""
function criu_check()
    ccall((:criu_check, libcriu), Cint, ())
end

"""
    criu_dump()

Checkpoint a process.
Returns 0 on success or negative error code on failure.
"""
function criu_dump()
    ccall((:criu_dump, libcriu), Cint, ())
end

"""
    criu_restore()

Restore a process.
Returns positive PID on success or negative error code on failure.
"""
function criu_restore()
    ccall((:criu_restore, libcriu), Cint, ())
end

"""
    criu_restore_child()

Restore a process as a child of the calling process.
Returns positive PID on success or negative error code on failure.
"""
function criu_restore_child()
    ccall((:criu_restore_child, libcriu), Cint, ())
end

end # module