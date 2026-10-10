#include <stdio.h>
#include <sys/mman.h>
#include <unistd.h>

int main(void) {
    size_t size = (size_t)64 << 30;
    char *p = mmap(NULL, size, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS | MAP_NORESERVE, -1, 0);
    if (p == MAP_FAILED) {
        size = (size_t)1 << 30;
        p = mmap(NULL, size, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS | MAP_NORESERVE, -1, 0);
    }
    if (p == MAP_FAILED) return 1;
    long page = sysconf(_SC_PAGESIZE);
    for (size_t i = 0; i < size; i += (size_t)page) p[i] = 1;
    puts("ESCAPED");
    return 0;
}
