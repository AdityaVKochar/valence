import os

cases = [5, 1, 1000, 200000]
os.makedirs("tests", exist_ok=True)
for i, n in enumerate(cases, 1):
    with open(f"tests/{i:02d}.in", "w") as f:
        f.write(f"{n}\n")
    with open(f"tests/{i:02d}.out", "w") as f:
        f.write("".join(f"{k}\n" for k in range(1, n + 1)))
