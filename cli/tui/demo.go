package tui

import (
	"fmt"
	"strings"
	"time"
)

// snippet is a canned question + code answer used by the fake backend and to
// seed demo history. The set varies in language and length on purpose:
// most fit on screen, the Towers of Hanoi one overflows a small terminal and
// has one very long comment line (to test wrapping), and the LAST snippet is
// a generated table taller than any terminal (to test scrolling). Tests rely
// on the last snippet being the tall one.
type snippet struct {
	Question string
	Code     string
}

var snippets = []snippet{
	{"how many molecules are in a mole? (avogadro's number)", `package main

import "fmt"

// Avogadro's constant: the number of particles in one mole of a substance.
// Since the 2019 SI redefinition this value is EXACT: it is what defines
// the mole, rather than something we measure.
const avogadro = 6.02214076e23 // particles per mole

// molesToParticles converts an amount of substance (mol) to a particle count.
func molesToParticles(mol float64) float64 {
	return mol * avogadro
}

func main() {
	// Water's molar mass is about 18.015 g/mol, so 36.03 g is 2 mol.
	grams, molarMass := 36.03, 18.015
	mol := grams / molarMass

	fmt.Printf("%.2f g of water = %.2f mol\n", grams, mol)

	// %e prints scientific notation. The answer is ~1.204428e+24, a number
	// far too big for an int, which is why we use float64 here.
	fmt.Printf("that is about %e molecules\n", molesToParticles(mol))
}`},

	{"write a tail-recursive factorial in go", `package main

import "fmt"

// factorial computes n! using TAIL recursion: the recursive call is the very
// last thing the function does, and the running product travels along in the
// accumulator (acc) instead of being multiplied on the way back up the stack.
//
// Call it as factorial(n, 1): the accumulator starts at 1, the identity for
// multiplication.
func factorial(n, acc uint64) uint64 {
	if n <= 1 {
		return acc // base case: the answer has been built up in acc
	}
	return factorial(n-1, acc*n) // tail call: nothing left to do after it
}

// Caveat: some languages (Scheme, Haskell, Rust with luck) optimise tail
// calls into a loop, so the stack never grows. Go does NOT, so deep tail
// recursion in Go still uses one stack frame per call. The equivalent loop
// is what the compiler would ideally produce:
func factorialLoop(n uint64) uint64 {
	acc := uint64(1)
	for ; n > 1; n-- {
		acc *= n
	}
	return acc
}

func main() {
	// 20! = 2432902008176640000 is the largest factorial that fits in a
	// uint64; 21! overflows silently, so use math/big beyond that.
	fmt.Println(factorial(20, 1))
	fmt.Println(factorialLoop(20))
}`},

	{"python tail recursion without blowing the stack (trampoline)", `# Python has no tail-call optimisation, so sum_to(100_000) written naively
# would hit the recursion limit (about 1000 frames). A TRAMPOLINE fixes that:
# instead of calling itself, the function returns a thunk (a zero-argument
# function) describing the NEXT step, and a loop bounces between them.

def trampoline(f, *args):
    result = f(*args)
    while callable(result):  # keep bouncing until we get a real value
        result = result()
    return result


def sum_to(n, acc=0):
    if n == 0:
        return acc
    # Don't recurse. Hand back "the rest of the work" as a thunk.
    return lambda: sum_to(n - 1, acc + n)


print(trampoline(sum_to, 100_000))  # 5000050000, with constant stack depth

# Gotcha: this breaks if your real result is itself callable, because the
# loop can't tell a finished answer from a thunk. Wrap results in a class
# if that matters.`},

	{"fibonacci in haskell with accumulators", `-- Naive fib calls itself twice per step: exponential time. This version is
-- linear AND tail recursive by carrying the last two values forward.

fib :: Integer -> Integer
fib n = go n 0 1
  where
    -- go k a b: k steps remain; a = F(i), b = F(i+1)
    go 0 a _ = a
    go k a b = go (k - 1) b (a + b) -- tail call; GHC compiles it to a loop

main :: IO ()
main = print (fib 90) -- 2880067194370816120 (Integer never overflows)`},

	{"collatz conjecture step counter in rust", `// The Collatz conjecture: start with any positive integer. If it's even,
// halve it; if it's odd, triple it and add one. Repeat. Conjecture: you
// always reach 1. Nobody has proved it, and it has been checked up to about
// 2^68, so this loop is "safe" in practice but not in theory.

fn collatz_steps(mut n: u64) -> u32 {
    let mut steps = 0;
    while n != 1 {
        n = if n % 2 == 0 { n / 2 } else { 3 * n + 1 };
        steps += 1;
    }
    steps
}

fn main() {
    // 27 is the famous small troublemaker: it climbs to 9232 before it
    // finally falls back to 1, taking 111 steps.
    println!("27 takes {} steps", collatz_steps(27));
}`},

	{"memoize a recursive fibonacci in typescript", `// Plain recursive fib recomputes the same subproblems over and over
// (fib(40) makes over 300 million calls). Caching each result the first time
// we compute it, MEMOIZATION, turns that into a linear number of calls.

const memo = new Map<number, bigint>();

export function fib(n: number): bigint {
  if (n < 2) return BigInt(n); // base cases: fib(0)=0, fib(1)=1

  const cached = memo.get(n);
  if (cached !== undefined) return cached; // already solved, reuse it

  const result = fib(n - 1) + fib(n - 2);
  memo.set(n, result);
  return result;
}

// bigint because fib(100) = 354224848179261915075 is far beyond 2^53, the
// largest integer a normal JS number can hold exactly.
console.log(fib(100));`},

	{"estimate pi with random numbers (monte carlo)", `import random


def estimate_pi(samples: int = 1_000_000) -> float:
    """Throw darts at a 1x1 square; count those inside the quarter circle.

    The quarter circle of radius 1 has area pi/4 and the square has area 1,
    so (darts inside) / (all darts) approaches pi/4. Multiply by 4.
    """
    inside = 0
    for _ in range(samples):
        x, y = random.random(), random.random()  # a dart in [0,1) x [0,1)
        if x * x + y * y <= 1:  # inside the circle? (Pythagoras)
            inside += 1
    return 4 * inside / samples


# Accuracy improves slowly: error shrinks like 1/sqrt(samples), so 100x more
# darts buys only one extra digit. Fun, but use a series for real work.
print(estimate_pi())  # ~3.14`},

	{"sieve of eratosthenes in go", `package main

import "fmt"

// sieve returns every prime <= limit using the Sieve of Eratosthenes
// (about 240 BC): for each prime found, cross off all of its multiples.
// Whatever is never crossed off must be prime.
func sieve(limit int) []int {
	composite := make([]bool, limit+1) // false means "not crossed off (yet)"
	var primes []int

	for i := 2; i <= limit; i++ {
		if composite[i] {
			continue // a smaller prime already crossed this one off
		}
		primes = append(primes, i)

		// Start at i*i: any smaller multiple (like 2*i) has a smaller prime
		// factor and was already crossed off. This is the key optimisation.
		for j := i * i; j <= limit; j += i {
			composite[j] = true
		}
	}
	return primes
}

func main() {
	fmt.Println(sieve(50)) // [2 3 5 7 11 13 17 19 23 29 31 37 41 43 47]
}`},

	{"towers of hanoi, recursively", `package main

import "fmt"

// hanoi moves n disks from peg "from" to peg "to", using "via" as the spare.
// The rules: move one disk at a time, and never put a larger disk on a
// smaller one.
//
// The recursive insight: to move n disks you must
//   1. move the top n-1 disks out of the way (onto the spare peg),
//   2. move the biggest disk to the destination,
//   3. move the n-1 disks back on top of it.
// Step 1 and 3 are the SAME problem with one fewer disk. That's recursion.
func hanoi(n int, from, to, via string, moves *int) {
	if n == 0 {
		return // base case: no disks, nothing to do
	}
	hanoi(n-1, from, via, to, moves) // 1. clear the way
	*moves++
	fmt.Printf("move disk %d: %s -> %s\n", n, from, to) // 2. biggest disk
	hanoi(n-1, via, to, from, moves) // 3. restack on top
}

func main() {
	var moves int
	hanoi(3, "A", "C", "B", &moves)

	// The minimum number of moves is 2^n - 1, so it doubles with every disk:
	// 3 disks = 7 moves, 20 disks = 1,048,575 moves, and 64 disks = about 1.8e19 moves. At one move per second that would take roughly 585 billion years, which is why the legend of the monks moving 64 golden disks promises the end of the world.
	fmt.Println("total moves:", moves) // 7
}`},

	{"sql recursive cte that generates fibonacci numbers", `-- A recursive CTE has two parts joined by UNION ALL:
--   1. an ANCHOR row to start from, and
--   2. a RECURSIVE step that reads the previous row and produces the next.
-- The database repeats step 2 until it produces no more rows.

WITH RECURSIVE fib(n, a, b) AS (
    SELECT 1, 0, 1                     -- anchor: n=1, F(n)=0, F(n+1)=1
    UNION ALL
    SELECT n + 1, b, a + b             -- step: slide the pair forward
    FROM fib
    WHERE n < 20                       -- the stop condition; without it, forever
)
SELECT n, a AS fibonacci
FROM fib;`},

	{"compute a square root with newton's method", `package main

import (
	"fmt"
	"math"
)

// sqrt approximates the square root of x using Newton's method.
//
// To solve z*z = x, start with a guess z and repeatedly improve it with
//     z = z - (z*z - x) / (2*z)
// which is the tangent-line trick from calculus. Each step roughly DOUBLES
// the number of correct digits, so a handful of iterations is plenty.
func sqrt(x float64) float64 {
	if x < 0 {
		return math.NaN() // no real square root
	}
	z := 1.0 // any positive starting guess works
	for i := 0; i < 20; i++ {
		z -= (z*z - x) / (2 * z)
	}
	return z
}

func main() {
	fmt.Println(sqrt(2))      // 1.414213562373095
	fmt.Println(math.Sqrt(2)) // 1.4142135623730951 (ours is off by 1 in the last digit)
}`},

	{"draw the mandelbrot set in the terminal", `# The Mandelbrot set: all complex numbers c for which z = z*z + c, starting
# from z = 0, stays bounded forever. We can't iterate forever, so we try 30
# times; if |z| ever exceeds 2, it is guaranteed to escape, so c is OUTSIDE.

for y in range(-12, 13):
    row = ""
    for x in range(-39, 22):
        c = complex(x / 20, y / 10)  # map the character grid onto the plane
        z = 0
        for _ in range(30):
            z = z * z + c
            if abs(z) > 2:           # escaped: not in the set
                row += " "
                break
        else:                        # loop never broke: assume it's in the set
            row += "#"
    print(row)`},

	{"precomputed fibonacci lookup table (long output)", fibTable()},
}

// fibTable builds a snippet taller than any terminal so scrolling is easy to
// test. The numbers are real: F(93) is the largest Fibonacci that fits in a
// uint64, so 80 entries are safe.
func fibTable() string {
	var b strings.Builder
	b.WriteString("// The first 80 Fibonacci numbers, F(0)..F(79), precomputed.\n")
	b.WriteString("// Each entry is the sum of the two before it.\n")
	b.WriteString("var fibs = [80]uint64{\n")
	a, c := uint64(0), uint64(1)
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&b, "\t%d, // F(%d)\n", a, i)
		a, c = c, a+c
	}
	b.WriteString("}")
	return b.String()
}

// DemoEntries returns the snippets as a plausible past history, oldest first,
// with timestamps spread over the last couple of weeks relative to now.
func DemoEntries(now time.Time) []Entry {
	entries := make([]Entry, len(snippets))
	for i, s := range snippets {
		age := time.Duration(len(snippets)-i)*31*time.Hour + time.Duration(i*7)*time.Minute
		entries[i] = Entry{Question: s.Question, Answer: s.Code, At: now.Add(-age)}
	}
	return entries
}

// NewMemStore returns an in-memory Store seeded with entries. Nothing touches
// disk, so demo sessions don't pollute real history.
func NewMemStore(seed []Entry) Store {
	return &memStore{entries: append([]Entry(nil), seed...)}
}
