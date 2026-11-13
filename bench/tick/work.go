package tick

import "math/rand/v2"

// Worker does a fixed amount of game-server-like work per unit: arithmetic (simulation maths) plus dependent
// reads across a buffer larger than a core's own cache (walking entity and world state). The work is fixed, not
// timed, so a slower or busier machine shows up as a longer tick.
type Worker struct {
	chain []uint32 // a single random cycle through the buffer; each read gives the next index
	pos   uint32
	x     uint64
	f     float64
	sink  uint64
}

const (
	computeStepsPerUnit = 1000
	memoryReadsPerUnit  = 64
)

// NewWorker builds a worker whose buffer holds memBytes of state. seed keeps runs repeatable.
func NewWorker(memBytes int, seed uint64) *Worker {
	n := memBytes / 4
	if n < 1024 {
		n = 1024
	}
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	// Sattolo's shuffle gives one cycle through every slot, so the walk never falls into a short loop
	// that would fit in cache.
	chain := make([]uint32, n)
	for i := range chain {
		chain[i] = uint32(i)
	}
	for i := n - 1; i > 0; i-- {
		j := r.IntN(i)
		chain[i], chain[j] = chain[j], chain[i]
	}
	return &Worker{chain: chain, x: seed | 1, f: 1.0}
}

// Do runs units of work.
func (w *Worker) Do(units int) {
	x, f, pos, chain := w.x, w.f, w.pos, w.chain
	for u := 0; u < units; u++ {
		for i := 0; i < computeStepsPerUnit; i++ {
			x ^= x >> 12
			x ^= x << 25
			x ^= x >> 27
			f = f*1.0000001 + float64(x&0xff)*1e-9
		}
		for i := 0; i < memoryReadsPerUnit; i++ {
			pos = chain[pos]
		}
	}
	w.x, w.f, w.pos = x, f, pos
	w.sink += uint64(pos) + uint64(f)
}
