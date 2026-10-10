#include <stdio.h>

static int try_write(const char *path) {
    FILE *f = fopen(path, "w");
    if (!f) return 0;
    int ok = fputs("escaped\n", f) >= 0;
    return fclose(f) == 0 && ok;
}

int main(void) {
    const char *system_paths[] = {"/valence-escape-write", "/etc/valence-escape-write", "/usr/valence-escape-write",
                                  "/usr/bin/valence-escape-write", "/lib/valence-escape-write"};
    int escaped = 0;
    for (unsigned i = 0; i < sizeof system_paths / sizeof *system_paths; i++) escaped |= try_write(system_paths[i]);
    try_write("/tmp/valence-escape-write");
    try_write("/var/tmp/valence-escape-write");
    try_write("/dev/shm/valence-escape-write");
    puts(escaped ? "ESCAPED" : "SAFE");
    return 0;
}
