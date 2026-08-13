package analyze

import (
	"context"
	"fmt"
	"runtime"
	"sort"
)

type PCMBlock struct {
	Index       int
	StartSample int64
	Samples     []float32
}

type AnalysisResult struct {
	Metrics       []WindowMetric
	SourceSamples int64
}

type analysisJob struct{ block PCMBlock }
type analysisResult struct {
	index   int
	metrics []WindowMetric
}

func WorkerCount(requested int) int {
	if requested > 0 {
		return requested
	}
	count := runtime.NumCPU()
	if count < 1 {
		return 1
	}
	if count > 8 {
		return 8
	}
	return count
}

func AnalyzeBlocks(ctx context.Context, blocks <-chan PCMBlock, sampleRate, windowMS, requestedWorkers int) (AnalysisResult, error) {
	var allMetrics []WindowMetric
	sourceSamples, err := AnalyzeBlocksTo(ctx, blocks, sampleRate, windowMS, requestedWorkers,
		func(metrics []WindowMetric) { allMetrics = append(allMetrics, metrics...) })
	if err != nil {
		return AnalysisResult{}, err
	}
	return AnalysisResult{Metrics: allMetrics, SourceSamples: sourceSamples}, nil
}

func AnalyzeBlocksTo(ctx context.Context, blocks <-chan PCMBlock, sampleRate, windowMS, requestedWorkers int,
	consume func([]WindowMetric)) (int64, error) {
	workers := WorkerCount(requestedWorkers)
	jobs := make(chan analysisJob, workers*2)
	results := make(chan analysisResult, workers*2)
	workerDone := make(chan struct{})
	for i := 0; i < workers; i++ {
		go func() {
			defer func() { workerDone <- struct{}{} }()
			for job := range jobs {
				metrics := Windows(job.block.Samples, sampleRate, windowMS)
				for i := range metrics {
					metrics[i].StartSample += job.block.StartSample
				}
				select {
				case results <- analysisResult{index: job.block.Index, metrics: metrics}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	producerDone := make(chan error, 1)
	go func() {
		defer close(jobs)
		for block := range blocks {
			if len(block.Samples) == 0 {
				continue
			}
			select {
			case jobs <- analysisJob{block: block}:
			case <-ctx.Done():
				producerDone <- ctx.Err()
				return
			}
		}
		producerDone <- nil
	}()

	go func() {
		for i := 0; i < workers; i++ {
			<-workerDone
		}
		close(results)
	}()

	ordered := make(map[int][]WindowMetric)
	var sourceSamples int64
	next := 0
	var firstErr error
	for result := range results {
		if _, exists := ordered[result.index]; exists {
			if firstErr == nil {
				firstErr = fmt.Errorf("duplicate analysis result for block %d", result.index)
			}
			continue
		}
		ordered[result.index] = result.metrics
		for _, metric := range result.metrics {
			end := metric.StartSample + int64(metric.Samples)
			if end > sourceSamples {
				sourceSamples = end
			}
		}
		for {
			metrics, ok := ordered[next]
			if !ok {
				break
			}
			delete(ordered, next)
			if consume != nil {
				consume(metrics)
			}
			next++
		}
	}
	if err := <-producerDone; err != nil && firstErr == nil {
		firstErr = err
	}
	if firstErr != nil {
		return 0, firstErr
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(ordered) != 0 {
		keys := make([]int, 0, len(ordered))
		for key := range ordered {
			keys = append(keys, key)
		}
		sort.Ints(keys)
		return 0, fmt.Errorf("analysis result gap before block %d", keys[0])
	}
	return sourceSamples, nil
}
