#include <fcntl.h>
#include <stdio.h>
#include <unistd.h>

int main(void) {
    char name[64];
    long created = 0;
    for (long i = 0; i < 200000; i++) {
        snprintf(name, sizeof name, "f%ld", i);
        int fd = open(name, O_CREAT | O_WRONLY, 0644);
        if (fd < 0) break;
        created++;
        if (i < 1000) continue;
        close(fd);
    }
    printf("SAFE\n");
    fprintf(stderr, "created %ld files\n", created);
    return 0;
}
