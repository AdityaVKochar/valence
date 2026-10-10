a, b = map(int, input().split())
blocks = [bytearray(64 << 20) for _ in range(12)]
print(a + b + len(blocks) - 12)
