#include <cstring>
#include <vector>

int main() {
    std::vector<char*> blocks;
    for (;;) {
        char* p = new char[1 << 20];
        std::memset(p, 1, 1 << 20);
        blocks.push_back(p);
    }
}
