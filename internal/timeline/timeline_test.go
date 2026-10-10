package timeline

import (
	"math"
	"math/rand"
	"testing"
)

func TestNewMergesAdjacentActions(t *testing.T) {
	got, err := New(10, []Segment{{0, 2, Keep}, {2, 4, Keep}, {4, 6, Cut}, {6, 10, Cut}})
	if err != nil {
		t.Fatal(err)
	}
	want := []Segment{{0, 4, Keep}, {4, 10, Cut}}
	assertSegments(t, got.Segments(), want)
}

func TestFromCutsEdgesAndConsecutiveCuts(t *testing.T) {
	got, err := FromCuts(100, []Segment{{0, 10, Cut}, {10, 20, Cut}, {50, 70, Cut}, {65, 80, Cut}})
	if err != nil {
		t.Fatal(err)
	}
	assertSegments(t, got.Segments(), []Segment{{0, 20, Cut}, {20, 50, Keep}, {50, 80, Cut}, {80, 100, Keep}})
}

func TestNewRejectsBadCoverageAndDuration(t *testing.T) {
	for _, segments := range [][]Segment{
		{{0, 0, Keep}},
		{{1, 2, Keep}},
		{{0, 3, Keep}, {2, 4, Cut}},
		{{0, 3, Keep}},
	} {
		if _, err := New(4, segments); err == nil {
			t.Fatalf("New(%v) accepted invalid timeline", segments)
		}
	}
}

func TestMappingLookup(t *testing.T) {
	timeline, err := New(100, []Segment{{0, 20, Keep}, {20, 50, Cut}, {50, 100, Keep}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		source, output TimeNS
	}{
		{0, 0}, {19, 19}, {20, 20}, {49, 20}, {50, 20}, {99, 69}, {100, 70},
	} {
		if got := timeline.MapSourceToOutput(test.source); got != test.output {
			t.Errorf("MapSourceToOutput(%d)=%d, want %d", test.source, got, test.output)
		}
	}
	for source := range TimeNS(100) {
		fromBinary, bi, ok := timeline.Lookup(source)
		if !ok || bi < 0 || bi >= len(timeline.Segments()) {
			t.Fatalf("lookup failed at %d: binary=%v/%d/%v", source, fromBinary, bi, ok)
		}
	}
}

func TestSampleBoundaryConversions(t *testing.T) {
	if got := SampleIndexAtOrAfter(20_833_334, 48_000); got != 1001 {
		t.Fatalf("ceil sample index=%d", got)
	}
	if got := TimeForSamples(48_000, 48_000); got != 1_000_000_000 {
		t.Fatalf("sample time=%d", got)
	}
}

func TestTimeNSFormatting(t *testing.T) {
	cases := []struct {
		input TimeNS
		want  string
	}{
		{-1000, "00:00:00.000"},
		{0, "00:00:00.000"},
		{500_000_000, "00:00:00.500"},
		{1_000_000_000, "00:00:01.000"},
		{61_500_000_000, "00:01:01.500"},
		{3661_234_000_000, "01:01:01.234"},
	}
	for _, tc := range cases {
		if got := tc.input.String(); got != tc.want {
			t.Errorf("TimeNS(%d).String() = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestFuzzLikeMappingProperties(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for range 500 {
		end := TimeNS(1 + rng.Intn(10_000))
		cuts := make([]Segment, rng.Intn(20))
		for i := range cuts {
			a := TimeNS(rng.Int63n(int64(end)))
			b := TimeNS(rng.Int63n(int64(end)))
			if a > b {
				a, b = b, a
			}
			cuts[i] = Segment{Start: a, End: b, Action: Cut}
		}
		timeline, err := FromCuts(end, cuts)
		if err != nil {
			t.Fatal(err)
		}
		previous := TimeNS(0)
		for source := TimeNS(0); source <= end; source++ {
			mapped := timeline.MapSourceToOutput(source)
			if mapped < previous || mapped < 0 || mapped > timeline.OutputDuration() {
				t.Fatalf("non-monotonic map: source=%d mapped=%d previous=%d end=%d", source, mapped, previous, timeline.OutputDuration())
			}
			previous = mapped
		}
	}
	_ = math.MaxInt64
}

func FuzzTimelineMapping(f *testing.F) {
	f.Add(int64(1000), int64(100), int64(400), int64(700))
	f.Add(int64(1), int64(0), int64(0), int64(1))
	f.Fuzz(func(t *testing.T, sourceEnd, cutStart, cutEnd, probe int64) {
		if sourceEnd <= 0 {
			return
		}
		cutStart = nonnegativeModulo(cutStart, sourceEnd)
		cutEnd = nonnegativeModulo(cutEnd, sourceEnd)
		if cutStart > cutEnd {
			cutStart, cutEnd = cutEnd, cutStart
		}
		value, err := FromCuts(TimeNS(sourceEnd), []Segment{{TimeNS(cutStart), TimeNS(cutEnd), Cut}})
		if err != nil {
			t.Fatal(err)
		}
		previous := TimeNS(0)
		for i := int64(0); i <= 32; i++ {
			source := TimeNS(i) * TimeNS(sourceEnd) / 32
			mapped := value.MapSourceToOutput(source)
			if mapped < previous || mapped < 0 || mapped > value.OutputDuration() {
				t.Fatalf("map decreased or escaped: source=%d mapped=%d previous=%d", source, mapped, previous)
			}
			previous = mapped
		}
		_ = probe
	})
}

func FuzzSampleBoundary(f *testing.F) {
	f.Add(int64(1_000_000_000), 48_000)
	f.Add(int64(0), 1)
	f.Fuzz(func(t *testing.T, nanoseconds int64, sampleRate int) {
		if nanoseconds < 0 || sampleRate <= 0 || sampleRate > 1_000_000 {
			return
		}
		atOrAfter := SampleIndexAtOrAfter(TimeNS(nanoseconds), sampleRate)
		if atOrAfter < 0 {
			t.Fatalf("invalid boundary conversion: after=%d", atOrAfter)
		}
	})
}

func nonnegativeModulo(value, modulus int64) int64 {
	value %= modulus
	if value < 0 {
		value += modulus
	}
	return value
}

func assertSegments(t *testing.T, got, want []Segment) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("segments=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("segment %d=%v, want %v", i, got[i], want[i])
		}
	}
}
