import os
import random

random.seed("sum-of-array")
cases = [[1, 2, 3, 4, 5], [-7], [10**9] * 3]
cases.append([random.randint(-10**9, 10**9) for _ in range(1000)])
cases.append([10**9] * 20000)
cases.append([random.randint(-10**9, 10**9) for _ in range(200000)])
os.makedirs("tests", exist_ok=True)
for i, a in enumerate(cases, 1):
    with open(f"tests/{i:02d}.in", "w") as f:
        f.write(f"{len(a)}\n{' '.join(map(str, a))}\n")
    with open(f"tests/{i:02d}.out", "w") as f:
        f.write(f"{sum(a)}\n")
