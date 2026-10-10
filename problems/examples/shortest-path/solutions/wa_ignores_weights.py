import sys
from collections import deque

data = sys.stdin.read().split()
n, m = int(data[0]), int(data[1])
adj = [[] for _ in range(n + 1)]
for i in range(m):
    u, v = int(data[2 + 3 * i]), int(data[3 + 3 * i])
    adj[u].append(v)
dist = [-1] * (n + 1)
dist[1] = 0
q = deque([1])
while q:
    u = q.popleft()
    for v in adj[u]:
        if dist[v] == -1:
            dist[v] = dist[u] + 1
            q.append(v)
print(dist[n])
