import os

watch = (b"judge-worker", b"escape.test", b"postgres", b"dockerd", b"containerd", b"systemd", b"sshd", b"isolate")
escaped = False
try:
    pids = [p for p in os.listdir("/proc") if p.isdigit()]
except OSError:
    pids = []
for pid in pids:
    try:
        with open("/proc/%s/cmdline" % pid, "rb") as f:
            cmd = f.read()
    except OSError:
        continue
    if any(w in cmd for w in watch):
        escaped = True
if len(pids) > 20:
    escaped = True
print("ESCAPED" if escaped else "SAFE")
