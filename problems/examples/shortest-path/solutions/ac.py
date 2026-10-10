import heapq
import sys

data = sys.stdin.read().split()
n, m = int(data[0]), int(data[1])
adj = [[] for _ in range(n + 1)]
for i in range(m):
    u, v, w = (int(x) for x in data[2 + 3 * i : 5 + 3 * i])
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
print(-1 if dist[n] is None else dist[n])
