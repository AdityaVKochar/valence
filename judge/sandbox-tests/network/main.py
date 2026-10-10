import socket

targets = [("1.1.1.1", 53), ("8.8.8.8", 443), ("127.0.0.1", 5432), ("127.0.0.1", 8090), ("169.254.169.254", 80)]
escaped = False
for host, port in targets:
    try:
        socket.create_connection((host, port), timeout=0.3).close()
        escaped = True
    except OSError:
        pass
try:
    socket.getaddrinfo("example.com", 80)
    escaped = True
except OSError:
    pass
print("ESCAPED" if escaped else "SAFE")
