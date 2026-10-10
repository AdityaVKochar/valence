# problems/examples

Sample problems in the [package format](../../docs/problem-format.md). `make seed` imports all of them, and the judge tests run every file in each `solutions/` folder and check that it gets the verdict its name starts with.

| Problem | Checker | What it exercises |
|---|---|---|
| `aplusb` | tokens | A solution for every verdict and every language |
| `sum-of-array` | tokens | A 200,000-number input and 64-bit sums |
| `reverse-string` | exact | Byte-exact output |
| `shortest-path` | tokens | Dijkstra on a small graph |
| `circle-area` | float:1e-6 | Floating-point answers |
| `output-heavy` | tokens | 1.3 MB of output |

Each package has a `generator.py` that wrote its tests. Run it from the package folder to regenerate them.
