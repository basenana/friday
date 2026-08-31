package actor

import (
	"sync/atomic"
	"time"
)

// idGen produces short, monotonic-ish identifiers per actor process.
// Format: <prefix>-<unix-nano-base36>. They are not cryptographically
// unique; uniqueness only needs to hold within a single actor run.
type idGen struct {
	counter atomic.Int64
}

// Next returns a new identifier with the given prefix.
func (g *idGen) Next(prefix string) string {
	n := g.counter.Add(1)
	return prefix + "-" + base36(time.Now().UnixNano()) + "-" + base36(n)
}

// globalIDGenerator is shared by all Translators in this process.
var globalIDGenerator = &idGen{}

// base36 renders a non-negative int64 compactly.
func base36(n int64) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = digits[int(n%36)]
		n /= 36
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
