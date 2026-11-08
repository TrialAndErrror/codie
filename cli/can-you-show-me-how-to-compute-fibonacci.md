# Can you show me how to compute fibonacci sequence recursively in python

Fibonacci fits recursion naturally: each number is the sum of the two before it, and the recursion stops at zero and one. I'll build the plain recursive function first, then add caching so repeated work is eliminated, and finish with how to run it.

The function below is a direct translation of the definition. When `n` is zero or one it returns `n` itself, which acts as the base case, and any larger value recurses on the two previous numbers.

```python
def fib(n):
    if n < 2:
        return n
    return fib(n - 1) + fib(n - 2)
```

The plain version is fine for small inputs, but it recomputes the same values over and over, which makes it exponential in time. Wrapping it with `functools.lru_cache` stores every result, so each `n` is computed only once and the whole thing runs in linear time.

```python
from functools import lru_cache

@lru_cache(maxsize=None)
def fib(n):
    if n < 2:
        return n
    return fib(n - 1) + fib(n - 2)
```

On Python 3.9 and later you can use `@functools.cache` instead; it behaves identically for a function like this with no arguments needing eviction. Put the function in a file, then add a small snippet that calls it to check the output.

```python
if __name__ == "__main__":
    print([fib(i) for i in range(10)])
```

Running the file prints the first ten numbers of the sequence in one line. Call `fib(30)` or higher and it returns instantly thanks to the cache.

```python
# fib.py
print(fib(30))  # 832040
```
