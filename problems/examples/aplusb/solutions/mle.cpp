#include <cstring>
#include <iostream>
#include <vector>

int main() {
    std::vector<char> v(384u << 20);
    std::memset(v.data(), 1, v.size());
    long long a, b;
    std::cin >> a >> b;
    std::cout << a + b + v[12345] - 1 << "\n";
}
