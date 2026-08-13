package analyze

import "testing"

func BenchmarkSilenceWindowAnalysis(b *testing.B) {
	samples := repeatedSamples(480, 0.08)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = AnalyzeWindow(samples)
	}
}

func BenchmarkOrderedWorkerMerge(b *testing.B) {
	blocks := make([]PCMBlock, 64)
	for i := range blocks {
		blocks[i] = PCMBlock{Index: i, StartSample: int64(i * 9600), Samples: repeatedSamples(9600, 0.05)}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := AnalyzeBlocks(b.Context(), blockChannel(blocks), 48000, 10, 4); err != nil {
			b.Fatal(err)
		}
	}
}
