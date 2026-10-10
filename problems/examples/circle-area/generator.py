import math
import os
import random

random.seed("circle-area")
cases = ["1", "2.5", "1000", "0.001"] + [f"{random.uniform(0.001, 1000):.4f}" for _ in range(4)]
os.makedirs("tests", exist_ok=True)
for i, r in enumerate(cases, 1):
    with open(f"tests/{i:02d}.in", "w") as f:
        f.write(r + "\n")
    with open(f"tests/{i:02d}.out", "w") as f:
        f.write(f"{math.pi * float(r) ** 2:.12f}\n")
