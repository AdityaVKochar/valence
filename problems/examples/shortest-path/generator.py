import heapq
import os
import random

random.seed("shortest-path")


def solve(n, edges):
    adj = [[] for _ in range(n + 1)]
    for u, v, w in edges:
        adj[u].append((v, w))
    dist = [None] * (n + 1)
    pq = [(0, 1)]
    while pq:
        d, u = heapq.heappop(pq)
        if dist[u] is not None:
            continue
        dist[u] = d
        for v, w in adj[u]:
            if dist[v] is None:
                heapq.heappush(pq, (d + w, v))
    return -1 if dist[n] is None else dist[n]


cases = [
    (4, [(1, 2, 1), (2, 4, 5), (1, 3, 2), (3, 4, 1)]),
    (3, [(2, 3, 7)]),
    (2, [(1, 2, 10), (1, 2, 3)]),
    (5, [(1, 2, 1), (2, 3, 1), (3, 4, 1), (4, 5, 1), (1, 5, 10)]),
]
for n, m in [(1000, 5000), (1000, 1200), (500, 5000)]:
    edges = [(random.randint(1, n), random.randint(1, n), random.randint(1, 10**6)) for _ in range(m)]
    cases.append((n, edges))
os.makedirs("tests", exist_ok=True)
for i, (n, edges) in enumerate(cases, 1):
    with open(f"tests/{i:02d}.in", "w") as f:
        f.write(f"{n} {len(edges)}\n")
        f.writelines(f"{u} {v} {w}\n" for u, v, w in edges)
    with open(f"tests/{i:02d}.out", "w") as f:
        f.write(f"{solve(n, edges)}\n")
