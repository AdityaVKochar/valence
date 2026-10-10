#include <cstdio>

int main() {
    static char line[4096];
    for (int i = 0; i < 4095; i++) line[i] = '7';
    line[4095] = '\n';
    for (;;) fwrite(line, 1, sizeof line, stdout);
}
