import os
import random

random.seed("aplusb")
cases = [(1, 2), (-5, 5), (10**18, 10**18), (-(10**18), -(10**18)), (123456789, -987654321)]
cases += [(random.randint(-10**18, 10**18), random.randint(-10**18, 10**18)) for _ in range(5)]
os.makedirs("tests", exist_ok=True)
for i, (a, b) in enumerate(cases, 1):
    with open(f"tests/{i:02d}.in", "w") as f:
        f.write(f"{a} {b}\n")
    with open(f"tests/{i:02d}.out", "w") as f:
        f.write(f"{a + b}\n")
