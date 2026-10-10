#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

int main(int argc, char **argv) {
    if (argc > 1) {
        puts("SAFE");
        return 0;
    }
    size_t n = 100000;
    char **args = calloc(n + 2, sizeof *args);
    char *arg = malloc(1024);
    if (!args || !arg) return 1;
    memset(arg, 'a', 1023);
    arg[1023] = 0;
    args[0] = argv[0];
    for (size_t i = 1; i <= n; i++) args[i] = arg;
    execv("/proc/self/exe", args);
    puts("SAFE");
    return 0;
}
