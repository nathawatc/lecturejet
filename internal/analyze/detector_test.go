package analyze

import (
	"math"
	"testing"

	"codeberg.org/nathawat/lecturejet/internal/config"
	"codeberg.org/nathawat/lecturejet/internal/timeline"
)

func TestDetectorPreservesShortSilenceAndCutsLongSilence(t *testing.T) {
	cfg := config.Default().Silence
	detector, err := NewDetector(cfg, 1000)
	if err != nil {
		t.Fatal(err)
	}
	speech := WindowMetric{Samples: 100, RMS: 0.2, Peak: 0.3}
	quiet := WindowMetric{Samples: 100, RMS: 0, Peak: 0}
	speech.StartSample = 0
	detector.Consume([]WindowMetric{speech})
	for i := range 4 {
		quiet.StartSample = int64((i + 1) * 100)
		detector.Consume([]WindowMetric{quiet})
	}
	shortCuts := detector.Finish(500)
	if len(shortCuts) != 0 {
		t.Fatalf("short silence cuts=%v", shortCuts)
	}

	detector, err = NewDetector(cfg, 1000)
	if err != nil {
		t.Fatal(err)
	}
	speech.StartSample = 0
	detector.Consume([]WindowMetric{speech})
	for i := range 50 {
		quiet.StartSample = int64((i + 1) * 100)
		detector.Consume([]WindowMetric{quiet})
	}
	speech.StartSample = 5100
	detector.Consume([]WindowMetric{speech})
	cuts := detector.Finish(5200)
	if len(cuts) != 1 || cuts[0].Action != timeline.Cut || cuts[0].Start != 250_000_000 || cuts[0].End != 4_950_000_000 {
		t.Fatalf("long silence cuts=%v", cuts)
	}
}

func TestAudioAboveThresholdEndsSilenceCandidate(t *testing.T) {
	detector, err := NewDetector(config.Default().Silence, 1000)
	if err != nil {
		t.Fatal(err)
	}
	quiet := WindowMetric{Samples: 100, RMS: 0, Peak: 0}
	band := WindowMetric{Samples: 100, RMS: 0.015, Peak: 0.015}
	speech := WindowMetric{Samples: 100, RMS: 0.2, Peak: 0.2}
	speech.StartSample = 0
	detector.Consume([]WindowMetric{speech})
	for i := range 3 {
		quiet.StartSample = int64((i + 1) * 100)
		detector.Consume([]WindowMetric{quiet})
	}
	band.StartSample = 400
	detector.Consume([]WindowMetric{band})
	speech.StartSample = 500
	detector.Consume([]WindowMetric{speech})
	if cuts := detector.Finish(800); len(cuts) != 0 {
		t.Fatalf("audio above threshold created unexpected cut: %v", cuts)
	}
}

func TestDetectorMarginShrinksCutToProtectSpeech(t *testing.T) {
	cfg := config.Default().Silence
	cfg.MarginMS = 100
	detector, err := NewDetector(cfg, 1000)
	if err != nil {
		t.Fatal(err)
	}
	speech := WindowMetric{StartSample: 0, Samples: 100, RMS: 0.2, Peak: 0.2}
	quiet := WindowMetric{Samples: 100, RMS: 0, Peak: 0}
	detector.Consume([]WindowMetric{speech})
	for i := range 6 {
		quiet.StartSample = int64(100 + i*100)
		detector.Consume([]WindowMetric{quiet})
	}
	speech.StartSample = 700
	detector.Consume([]WindowMetric{speech})
	cuts := detector.Finish(800)
	if len(cuts) != 1 || cuts[0].Start != 200_000_000 || cuts[0].End != 600_000_000 {
		t.Fatalf("margin cut=%v", cuts)
	}
}

func TestLowVolumeSpeechNearThresholdEndsCandidate(t *testing.T) {
	cfg := config.Default().Silence
	cfg.MarginMS = 0
	detector, err := NewDetector(cfg, 1000)
	if err != nil {
		t.Fatal(err)
	}
	speech := WindowMetric{StartSample: 0, Samples: 100, RMS: 0.2, Peak: 0.2}
	quiet := WindowMetric{Samples: 100, RMS: 0, Peak: 0}
	nearThreshold := WindowMetric{StartSample: 600, Samples: 100, RMS: 0.015, Peak: 0.015}
	detector.Consume([]WindowMetric{speech})
	for i := range 5 {
		quiet.StartSample = int64(100 + i*100)
		detector.Consume([]WindowMetric{quiet})
	}
	detector.Consume([]WindowMetric{nearThreshold})
	cuts := detector.Finish(700)
	if len(cuts) != 1 || cuts[0].Start != 100_000_000 || cuts[0].End != 600_000_000 {
		t.Fatalf("near-threshold speech was removed: %v", cuts)
	}
}

func TestAudioJustAboveThresholdEndsSilenceCandidate(t *testing.T) {
	cfg := config.Default().Silence
	cfg.MarginMS = 0
	detector, err := NewDetector(cfg, 1000)
	if err != nil {
		t.Fatal(err)
	}
	thresholdLevel := float32(math.Pow(10, cfg.ThresholdDB/20))
	justAbove := thresholdLevel + 0.0001

	speech := WindowMetric{StartSample: 0, Samples: 100, RMS: 0.2, Peak: 0.2}
	quiet := WindowMetric{Samples: 100, RMS: 0, Peak: 0}
	aboveThreshold := WindowMetric{StartSample: 600, Samples: 100, RMS: justAbove, Peak: justAbove}

	detector.Consume([]WindowMetric{speech})
	for i := range 5 {
		quiet.StartSample = int64(100 + i*100)
		detector.Consume([]WindowMetric{quiet})
	}
	detector.Consume([]WindowMetric{aboveThreshold})
	cuts := detector.Finish(700)
	if len(cuts) != 1 || cuts[0].Start != 100_000_000 || cuts[0].End != 600_000_000 {
		t.Fatalf("audio just above threshold failed to end silence candidate: %v", cuts)
	}
}

func TestAudioExactlyAtThresholdEndsSilenceCandidate(t *testing.T) {
	cfg := config.Default().Silence
	cfg.MarginMS = 0
	detector, err := NewDetector(cfg, 1000)
	if err != nil {
		t.Fatal(err)
	}
	thresholdLevel := float32(math.Pow(10, cfg.ThresholdDB/20))

	speech := WindowMetric{StartSample: 0, Samples: 100, RMS: 0.2, Peak: 0.2}
	quiet := WindowMetric{Samples: 100, RMS: 0, Peak: 0}
	exactThreshold := WindowMetric{StartSample: 600, Samples: 100, RMS: thresholdLevel, Peak: thresholdLevel}

	detector.Consume([]WindowMetric{speech})
	for i := range 5 {
		quiet.StartSample = int64(100 + i*100)
		detector.Consume([]WindowMetric{quiet})
	}
	detector.Consume([]WindowMetric{exactThreshold})
	cuts := detector.Finish(700)
	if len(cuts) != 1 || cuts[0].Start != 100_000_000 || cuts[0].End != 600_000_000 {
		t.Fatalf("audio exactly at threshold failed to end silence candidate: %v", cuts)
	}
}

func TestSilenceExactlyAtMinimumIsRemoved(t *testing.T) {
	cfg := config.Default().Silence
	cfg.MarginMS = 0
	detector, err := NewDetector(cfg, 1000)
	if err != nil {
		t.Fatal(err)
	}
	speech := WindowMetric{StartSample: 0, Samples: 100, RMS: 0.2, Peak: 0.2}
	quiet := WindowMetric{Samples: 100, RMS: 0, Peak: 0}
	detector.Consume([]WindowMetric{speech})
	for i := range 5 {
		quiet.StartSample = int64(100 + i*100)
		detector.Consume([]WindowMetric{quiet})
	}
	speech.StartSample = 600
	detector.Consume([]WindowMetric{speech})
	if cuts := detector.Finish(700); len(cuts) != 1 {
		t.Fatalf("minimum silence not removed: %v", cuts)
	}
}

func TestShortImpulseInsideSilenceDoesNotCreateAggressiveCut(t *testing.T) {
	cfg := config.Default().Silence
	cfg.MarginMS = 0
	detector, err := NewDetector(cfg, 1000)
	if err != nil {
		t.Fatal(err)
	}
	speech := WindowMetric{StartSample: 0, Samples: 100, RMS: 0.2, Peak: 0.2}
	quiet := WindowMetric{Samples: 100, RMS: 0, Peak: 0}
	impulse := WindowMetric{StartSample: 400, Samples: 100, RMS: 0.3, Peak: 0.8}
	detector.Consume([]WindowMetric{speech})
	for i := range 3 {
		quiet.StartSample = int64(100 + i*100)
		detector.Consume([]WindowMetric{quiet})
	}
	detector.Consume([]WindowMetric{impulse})
	for i := range 3 {
		quiet.StartSample = int64(500 + i*100)
		detector.Consume([]WindowMetric{quiet})
	}
	if cuts := detector.Finish(800); len(cuts) != 0 {
		t.Fatalf("short impulse split silence into cut=%v", cuts)
	}
}

func TestWindowMetricsRejectNonFiniteForDetection(t *testing.T) {
	metric := AnalyzeWindow([]float32{float32(math.NaN()), float32(math.Inf(1)), 0.5})
	if !IsFinite(metric.RMS) || !IsFinite(metric.Peak) {
		t.Fatalf("non-finite metric: %+v", metric)
	}
	if metric.Peak != 1 || metric.RMS != 1 {
		t.Fatalf("corrupt PCM metric=%+v", metric)
	}
}

func TestDetectorValidation(t *testing.T) {
	cfg := config.Default().Silence
	if _, err := NewDetector(cfg, 0); err == nil {
		t.Fatal("expected error for zero sample rate")
	}
	badCfg := cfg
	badCfg.MinSilenceMS = 5
	if _, err := NewDetector(badCfg, 1000); err == nil {
		t.Fatal("expected error for min silence < window")
	}
	badCfg = cfg
	badCfg.MarginMS = -1
	if _, err := NewDetector(badCfg, 1000); err == nil {
		t.Fatal("expected error for negative margin")
	}
	badCfg = cfg
	badCfg.ThresholdDB = math.NaN()
	if _, err := NewDetector(badCfg, 1000); err == nil {
		t.Fatal("expected error for NaN threshold")
	}
	badCfg = cfg
	badCfg.ThresholdDB = math.Inf(1)
	if _, err := NewDetector(badCfg, 1000); err == nil {
		t.Fatal("expected error for Inf threshold")
	}
}

func TestRMSPrecision(t *testing.T) {
	for _, n := range []int{1, 7, 8, 15, 16, 31, 32, 63, 480, 9600} {
		samples := make([]float32, n)
		for i := range samples {
			samples[i] = float32(math.Sin(float64(i)*0.05)) * 0.7
		}
		var expectedSum float64
		for _, s := range samples {
			expectedSum += float64(s) * float64(s)
		}
		expectedRMS := float32(math.Sqrt(expectedSum / float64(n)))

		gotGenericSum := sumSquaresGeneric(samples)
		gotGenericRMS := float32(math.Sqrt(gotGenericSum / float64(n)))
		diffGeneric := math.Abs(float64(expectedRMS - gotGenericRMS))
		if diffGeneric > 1e-6 {
			t.Fatalf("len %d: generic RMS diff=%v, want <= 1e-6", n, diffGeneric)
		}

		gotRMS := RMS(samples)
		diff := math.Abs(float64(expectedRMS - gotRMS))
		if diff > 1e-5 {
			t.Fatalf("len %d: vectorized RMS diff=%v, want <= 1e-5", n, diff)
		}
	}
}

func TestPeakAbsPrecision(t *testing.T) {
	if peakAbs(nil) != 0 || peakAbs([]float32{}) != 0 {
		t.Fatal("empty slice should return 0 peak")
	}

	for _, n := range []int{1, 2, 7, 8, 9, 15, 16, 31, 32, 33, 63, 64, 480, 9600} {
		samples := make([]float32, n)
		for i := range samples {
			val := float32(math.Sin(float64(i)*0.07)) * 0.95
			if i%3 == 0 {
				val = -val
			}
			samples[i] = val
		}
		expected := peakAbsGeneric(samples)
		got := peakAbs(samples)
		if got != expected {
			t.Fatalf("len %d: peakAbs=%v, want %v", n, got, expected)
		}
	}
}

func TestZeroCrossingRateCorrectness(t *testing.T) {
	if ZeroCrossingRate(nil) != 0 || ZeroCrossingRate([]float32{0.5}) != 0 {
		t.Fatal("empty or single-element slice should have 0 zero crossings")
	}

	for n := 2; n <= 100; n++ {
		samples := make([]float32, n)
		for i := range samples {
			if (i/2)%2 == 0 {
				samples[i] = 0.5
			} else {
				samples[i] = -0.5
			}
		}

		expected := 0
		for i := 0; i < n-1; i++ {
			a, b := samples[i], samples[i+1]
			if (a > 0 && b < 0) || (a < 0 && b > 0) {
				expected++
			}
		}

		gotGeneric := zcrGeneric(samples)
		if gotGeneric != expected {
			t.Fatalf("len %d: generic ZCR=%d, want %d", n, gotGeneric, expected)
		}

		got := ZeroCrossingRate(samples)
		if got != expected {
			t.Fatalf("len %d: ZeroCrossingRate=%d, want %d", n, got, expected)
		}
	}
}

func TestZeroAllocations(t *testing.T) {
	samples := repeatedSamples(480, 0.08)
	allocs := testing.AllocsPerRun(1000, func() {
		_ = AnalyzeWindow(samples)
	})
	if allocs != 0 {
		t.Fatalf("AnalyzeWindow allocs=%v, want 0", allocs)
	}

	allocsRMS := testing.AllocsPerRun(1000, func() {
		_ = RMS(samples)
	})
	if allocsRMS != 0 {
		t.Fatalf("RMS allocs=%v, want 0", allocsRMS)
	}

	allocsZCR := testing.AllocsPerRun(1000, func() {
		_ = ZeroCrossingRate(samples)
	})
	if allocsZCR != 0 {
		t.Fatalf("ZeroCrossingRate allocs=%v, want 0", allocsZCR)
	}
}
