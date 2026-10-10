import os
import random
import string

random.seed("reverse-string")
cases = ["hello", "a", "racecar", "ab" * 50000]
cases.append("".join(random.choice(string.ascii_lowercase) for _ in range(100000)))
os.makedirs("tests", exist_ok=True)
for i, s in enumerate(cases, 1):
    with open(f"tests/{i:02d}.in", "w") as f:
        f.write(s + "\n")
    with open(f"tests/{i:02d}.out", "w") as f:
        f.write(s[::-1] + "\n")
