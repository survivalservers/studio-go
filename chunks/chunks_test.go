package chunks

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"slices"
	"testing"
)

// goldenInput is a fixed stream: 12 MiB of SHA-256 counter output (the digests of 0, 1, 2 and so on as 8 big-endian
// bytes), 9 MiB of zeros (no cut until the largest chunk), then 1 MiB and 123 bytes counted from 2^32.
func goldenInput() []byte {
	count := func(from uint64, n int) []byte {
		var out []byte
		var b [8]byte
		for i := from; len(out) < n; i++ {
			binary.BigEndian.PutUint64(b[:], i)
			s := sha256.Sum256(b[:])
			out = append(out, s[:]...)
		}
		return out[:n]
	}
	data := count(0, 12<<20)
	data = append(data, make([]byte, 9<<20)...)
	return append(data, count(1<<32, 1<<20+123)...)
}

// TestGoldenCuts pins the cuts of the fixed stream: each chunk's size, and the SHA-256 of the lines "size sha256" of
// every chunk in order. The store's own chunker pins the same values, so a client names the chunks the store does.
// The values were worked out once, by a separate implementation of the same cutting rule.
func TestGoldenCuts(t *testing.T) {
	c := New(bytes.NewReader(goldenInput()))
	var sizes []int
	lines := sha256.New()
	for {
		chunk, err := c.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, len(chunk))
		fmt.Fprintf(lines, "%d %x\n", len(chunk), sha256.Sum256(chunk))
	}
	want := []int{1664767, 1590924, 1059173, 1742439, 2255175, 1286668, 4194304, 4194304, 4194304, 886737}
	if !slices.Equal(sizes, want) {
		t.Fatalf("chunk sizes %v, want %v", sizes, want)
	}
	if got := hex.EncodeToString(lines.Sum(nil)); got != "0603d7bf661f99ed3cebe2fad91d9137be53238a420252d9d1a7172c103b59b3" {
		t.Fatalf("the chunks' digest %s", got)
	}
}

// TestSeed: the table's seed is what its comment says it is.
func TestSeed(t *testing.T) {
	sum := sha256.Sum256([]byte("studio build chunker v1"))
	if got := binary.BigEndian.Uint64(sum[:8]); got != seed {
		t.Fatalf("seed %#x, the derivation gives %#x", uint64(seed), got)
	}
}

// TestShortFiles: an empty file has no chunks, and a file under the smallest chunk is one chunk.
func TestShortFiles(t *testing.T) {
	if _, err := New(bytes.NewReader(nil)).Next(); err != io.EOF {
		t.Fatalf("an empty file: %v", err)
	}
	small := bytes.Repeat([]byte("game"), 1000)
	c := New(bytes.NewReader(small))
	chunk, err := c.Next()
	if err != nil || !bytes.Equal(chunk, small) {
		t.Fatalf("a small file: %d bytes, %v", len(chunk), err)
	}
	if _, err := c.Next(); err != io.EOF {
		t.Fatalf("after the only chunk: %v", err)
	}
}

// TestAnEditMovesOnlyNearbyChunks: one byte changed in the middle of a file changes the chunk that holds it (and at
// most the one after, if a cut moved), and every other chunk stays the same.
func TestAnEditMovesOnlyNearbyChunks(t *testing.T) {
	data := goldenInput()[:12<<20]
	edited := slices.Clone(data)
	edited[6<<20] ^= 0xff
	names := func(b []byte) map[[32]byte]bool {
		out := map[[32]byte]bool{}
		c := New(bytes.NewReader(b))
		for {
			chunk, err := c.Next()
			if err == io.EOF {
				return out
			}
			if err != nil {
				t.Fatal(err)
			}
			out[sha256.Sum256(chunk)] = true
		}
	}
	before, after := names(data), names(edited)
	shared := 0
	for k := range after {
		if before[k] {
			shared++
		}
	}
	if len(after)-shared > 2 || shared == 0 {
		t.Fatalf("%d of %d chunks shared after a one-byte edit", shared, len(after))
	}
}
