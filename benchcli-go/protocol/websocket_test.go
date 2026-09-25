package protocol

import "testing"

// TestPipeline holds BenchPipeline's messages per write to its rules: -rpl when
// set, else what -rbs holds, at least one, at most -rr and -rl, and a divisor
// of -rr so every write is the same size.
func TestPipeline(t *testing.T) {
	for _, c := range []struct {
		name                                    string
		frameLen, rate, maxLen, pipeline, limit int
		want                                    int
	}{
		{"-rbs holds 15 frames, 10 divides -rr", 1032, 200, 16384, 0, 0, 10},
		{"-rpl as asked", 1032, 200, 16384, 8, 0, 8},
		{"-rpl past -rbs still as asked", 1032, 200, 1024, 20, 0, 20},
		{"-rpl lowered to divide -rr", 1032, 200, 16384, 7, 0, 5},
		{"-rpl no more than -rr", 1032, 20, 16384, 50, 0, 20},
		{"one frame bigger than -rbs", 65546, 200, 16384, 0, 0, 1},
		{"no more than -rl", 1032, 200, 16384, 0, 4, 4},
		{"-rl lowered to divide -rr", 1032, 200, 16384, 0, 3, 2},
	} {
		if got := Pipeline(c.frameLen, c.rate, c.maxLen, c.pipeline, c.limit); got != c.want {
			t.Errorf("%v: Pipeline = %v, want %v", c.name, got, c.want)
		}
	}

	buf, batch, tickRate := BatchBuffers(make([]byte, 65546), 200, 16384, 0, 0)
	if batch != 1 || tickRate != 200 || len(buf) != 65546 {
		t.Errorf("BatchBuffers of a frame bigger than -rbs = %v bytes, %v, %v; want 65546, 1, 200", len(buf), batch, tickRate)
	}
}
