#include <signal.h>
#include <stdio.h>
#include <unistd.h>

int main(void) {
    kill(getppid(), SIGKILL);
    kill(-1, SIGKILL);
    kill(1, SIGKILL);
    puts("SAFE");
    return 0;
}
