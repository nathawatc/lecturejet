package analyze

import "testing"

func BenchmarkSilenceWindowAnalysis(b *testing.B) {
	samples := repeatedSamples(480, 0.08)
	b.SetBytes(int64(len(samples) * 4))
	b.ResetTimer()
	for b.Loop() {
		_ = AnalyzeWindow(samples)
	}
}

func BenchmarkAnalyzeWindow(b *testing.B) {
	samples := repeatedSamples(480, 0.08)
	b.SetBytes(int64(len(samples) * 4))
	b.ResetTimer()
	for b.Loop() {
		_ = AnalyzeWindow(samples)
	}
}

func BenchmarkPeakAbs_480(b *testing.B) {
	samples := repeatedSamples(480, 0.08)
	b.SetBytes(int64(len(samples) * 4))
	b.ResetTimer()
	for b.Loop() {
		_ = peakAbs(samples)
	}
}

func BenchmarkPeakAbs_480_Generic(b *testing.B) {
	samples := repeatedSamples(480, 0.08)
	b.SetBytes(int64(len(samples) * 4))
	b.ResetTimer()
	for b.Loop() {
		_ = peakAbsGeneric(samples)
	}
}

func BenchmarkRMS_Window(b *testing.B) {
	samples := repeatedSamples(480, 0.08)
	b.SetBytes(int64(len(samples) * 4))
	b.ResetTimer()
	for b.Loop() {
		_ = RMS(samples)
	}
}

func BenchmarkRMS_1Hour(b *testing.B) {
	// 96,000 samples = 2 seconds of 48 kHz audio per block
	const blockSamples = 96000
	samples := repeatedSamples(blockSamples, 0.08)
	b.SetBytes(int64(len(samples) * 4))
	b.ResetTimer()
	for b.Loop() {
		_ = RMS(samples)
	}
}

func BenchmarkZCR_Window(b *testing.B) {
	samples := make([]float32, 480)
	for i := range samples {
		if i%2 == 0 {
			samples[i] = 0.08
		} else {
			samples[i] = -0.08
		}
	}
	b.SetBytes(int64(len(samples) * 4))
	b.ResetTimer()
	for b.Loop() {
		_ = ZeroCrossingRate(samples)
	}
}

func BenchmarkOrderedWorkerMerge(b *testing.B) {
	blocks := make([]PCMBlock, 64)
	for i := range blocks {
		blocks[i] = PCMBlock{Index: i, StartSample: int64(i * 9600), Samples: repeatedSamples(9600, 0.05)}
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := AnalyzeBlocks(b.Context(), blockChannel(blocks), 48000, 10, 4); err != nil {
			b.Fatal(err)
		}
	}
}
