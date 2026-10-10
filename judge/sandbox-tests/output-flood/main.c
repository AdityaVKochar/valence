#include <stdio.h>
#include <string.h>

int main(void) {
    char buf[1 << 16];
    memset(buf, 'x', sizeof buf);
    for (;;) fwrite(buf, 1, sizeof buf, stdout);
}
