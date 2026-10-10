#include <stdio.h>
#include <unistd.h>

int main(void) {
    for (int i = 0; i < 10000; i++) {
        pid_t pid = fork();
        if (pid < 0) {
            puts("SAFE");
            return 0;
        }
        if (pid == 0) {
            for (;;) pause();
        }
    }
    puts("ESCAPED");
    return 0;
}
