// Package chunks cuts a build's files into the chunks the build store keeps. The cuts follow the content, so a change
// early in a file moves only the chunks around it and a new build shares every other chunk with the one before. They
// are exactly the store's own cuts: a client can name each chunk (the SHA-256 of its bytes) before it uploads, and
// send only the chunks the studio's store does not hold yet.
//
// Each file is cut on its own, from its first byte. A chunk is 1 to 4 MiB, about 2 MiB on average; a file's last
// chunk may be smaller.
package chunks

import (
	"bufio"
	"io"
)

const (
	// MinChunk is the smallest chunk, except for a file's last.
	MinChunk = 1 << 20
	// MaxChunk is the largest chunk.
	MaxChunk = 4 << 20
	// A cut after MinChunk when the rolling hash's low 20 bits are zero: about 2 MiB on average.
	cutMask = 1<<20 - 1
	// seed fills the rolling hash's byte table: the first 8 bytes (big-endian) of SHA-256 of "studio build chunker v1".
	seed = 0x8ee4d17db018914c
)

// gear is the rolling hash's byte table, filled from splitmix64. It must never change: the cuts, and so the chunks a
// new build shares with the last, depend on it.
var gear = func() (t [256]uint64) {
	x := uint64(seed)
	for i := range t {
		x += 0x9e3779b97f4a7c15
		z := x
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		t[i] = z ^ (z >> 31)
	}
	return t
}()

// Chunker cuts one file's bytes into chunks.
type Chunker struct {
	r   *bufio.Reader
	buf []byte
}

// New returns a Chunker that reads one file from r.
func New(r io.Reader) *Chunker {
	return &Chunker{r: bufio.NewReaderSize(r, 1<<20), buf: make([]byte, 0, MaxChunk)}
}

// Next returns the next chunk, valid until the following call, or io.EOF after the last. An empty file has no chunks.
func (c *Chunker) Next() ([]byte, error) {
	c.buf = c.buf[:0]
	var h uint64
	for {
		b, err := c.r.ReadByte()
		if err == io.EOF {
			if len(c.buf) == 0 {
				return nil, io.EOF
			}
			return c.buf, nil
		}
		if err != nil {
			return nil, err
		}
		c.buf = append(c.buf, b)
		h = h<<1 + gear[b]
		n := len(c.buf)
		if n >= MaxChunk || (n >= MinChunk && h&cutMask == 0) {
			return c.buf, nil
		}
	}
}
