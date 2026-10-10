#include <stdio.h>
#include <string.h>
#include <unistd.h>

static int readable(const char *target, const char *needle) {
    unlink("link");
    if (symlink(target, "link") != 0) return 0;
    FILE *f = fopen("link", "r");
    if (!f) return 0;
    char buf[4096];
    size_t n = fread(buf, 1, sizeof buf - 1, f);
    fclose(f);
    buf[n] = 0;
    return strstr(buf, needle) != NULL;
}

int main(void) {
    int escaped = readable("/etc/shadow", "root:") || readable("/proc/1/root/etc/shadow", "root:") ||
                  readable("/proc/1/environ", "INTERNAL_TOKEN") || readable("../../../../../etc/shadow", "root:");
    puts(escaped ? "ESCAPED" : "SAFE");
    return 0;
}
