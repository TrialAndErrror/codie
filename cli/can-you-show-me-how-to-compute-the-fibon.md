# Can you show me how to compute the fibonnacci sequence up to 100k entries using recursive functions in python

Computing 100,000 Fibonacci numbers with pure naive recursion is impossible in practice, because naive recursion takes exponential time. The trick is to keep the recursive structure but add memoization, then fill the sequence one index at a time so the recursion never goes more than a couple of frames deep.

First, here is the memoized recursive function. Put this in a file called `fib.py`. The dictionary `memo` stores already-computed values, so each recursive call either returns instantly from the cache or does O(1) work after its two recursive calls return.

```python
memo = {0: 0, 1: 1}

def fib(n):
    if n in memo:
        return memo[n]
    memo[n] = fib(n - 1) + fib(n - 2)
    return memo[n]
```

If you called `fib(100000)` directly, even with memoization, Python would try to recurse 100,000 frames deep and hit its recursion limit (around 1000 by default). The fix is to warm the cache from the bottom up: call `fib(i)` for each `i` in order. Since `fib(i - 1)` and `fib(i - 2)` are already cached at that point, each call only recurses two frames deep, so the recursion limit is never a problem.

```python
def fib_sequence(count):
    result = [0, 1]
    for i in range(2, count):
        result.append(fib(i))
    return result
```

One thing to know before running this: Fibonacci numbers grow enormous. `fib(100000)` has roughly 20,900 digits, so Python's big integers are doing real work and the whole run takes a while (several minutes on typical hardware, mostly in big-integer addition). Memory is also substantial, since the sequence stores around 100k numbers of thousands of digits each.

Here is the last part of `fib.py`, which builds the full sequence and prints a sample so you can sanity-check it without dumping 100k giant numbers into your terminal.

```python
if __name__ == "__main__":
    seq = fib_sequence(100_000)
    print(f"computed {len(seq)} entries")
    print(f"fib(10)      = {seq[10]}")
    print(f"fib(100)     = {seq[100]}")
    print(f"fib(100000)  has {len(str(seq[99999]))} digits")
```

Run it with `python fib.py`. If you ever do need a single huge `fib(n)` without warming the cache first, the safer tool is iterative addition rather than recursion, because deep recursion can crash the interpreter even when the limit is raised.
