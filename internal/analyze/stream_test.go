package analyze

import (
	"context"
	"reflect"
	"testing"
)

func TestAnalyzeBlocksDeterministicAcrossWorkerCounts(t *testing.T) {
	blocks := []PCMBlock{
		{Index: 0, StartSample: 0, Samples: repeatedSamples(9600, 0.2)},
		{Index: 1, StartSample: 9600, Samples: repeatedSamples(9600, 0)},
		{Index: 2, StartSample: 19200, Samples: repeatedSamples(9600, 0.1)},
	}
	one, err := AnalyzeBlocks(context.Background(), blockChannel(blocks), 48000, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	many, err := AnalyzeBlocks(context.Background(), blockChannel(blocks), 48000, 10, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(one, many) {
		t.Fatalf("worker results differ:\none=%+v\nmany=%+v", one, many)
	}
	if len(one.Metrics) != 60 || one.SourceSamples != 28800 {
		t.Fatalf("unexpected result: metrics=%d samples=%d", len(one.Metrics), one.SourceSamples)
	}
}

func blockChannel(blocks []PCMBlock) <-chan PCMBlock {
	channel := make(chan PCMBlock, len(blocks))
	for _, block := range blocks {
		channel <- block
	}
	close(channel)
	return channel
}

func repeatedSamples(count int, value float32) []float32 {
	samples := make([]float32, count)
	for i := range samples {
		samples[i] = value
	}
	return samples
}
