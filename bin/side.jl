using Marily.CriuJulia

# Example usage
pid = getpid()
image_path = mktempdir()

self_checkpoint(image_path, false, "./logfile.log")

wait()