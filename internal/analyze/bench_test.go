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
