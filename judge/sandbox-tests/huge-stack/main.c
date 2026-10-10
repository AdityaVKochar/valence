#include <stdio.h>
#include <string.h>

static unsigned long dive(unsigned long depth) {
    char frame[64 * 1024];
    memset(frame, (int)depth, sizeof frame);
    return frame[depth % sizeof frame] + dive(depth + 1);
}

int main(void) {
    printf("%lu\n", dive(0));
    return 0;
}
