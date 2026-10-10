#include <pthread.h>
#include <stdio.h>
#include <unistd.h>

static void *sleeper(void *arg) {
    (void)arg;
    for (;;) pause();
    return NULL;
}

int main(void) {
    pthread_attr_t attr;
    pthread_attr_init(&attr);
    pthread_attr_setstacksize(&attr, 64 * 1024);
    for (int i = 0; i < 5000; i++) {
        pthread_t t;
        if (pthread_create(&t, &attr, sleeper, NULL) != 0) {
            puts("SAFE");
            fflush(stdout);
            _exit(0);
        }
    }
    puts("ESCAPED");
    fflush(stdout);
    _exit(0);
}
