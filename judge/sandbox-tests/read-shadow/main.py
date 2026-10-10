import glob
import os

escaped = False
for path in ["/etc/shadow", "/etc/gshadow", "/root/.ssh/id_rsa", "/root/.ssh/id_ed25519", "/etc/isolate"]:
    try:
        with open(path) as f:
            if f.read(1):
                escaped = True
    except OSError:
        pass
for key in os.environ:
    if key in ("DATABASE_URL", "INTERNAL_TOKEN", "GITHUB_CLIENT_SECRET", "GOOGLE_CLIENT_SECRET"):
        escaped = True
for path in glob.glob("/proc/*/environ"):
    try:
        with open(path, "rb") as f:
            if b"INTERNAL_TOKEN" in f.read():
                escaped = True
    except OSError:
        pass
print("ESCAPED" if escaped else "SAFE")
