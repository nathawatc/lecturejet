package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"codeberg.org/nathawat/lecturejet/internal/analyze"
	"codeberg.org/nathawat/lecturejet/internal/audio"
	"codeberg.org/nathawat/lecturejet/internal/config"
	"codeberg.org/nathawat/lecturejet/internal/media"
	"codeberg.org/nathawat/lecturejet/internal/timeline"
)

type Result struct {
	Timeline    timeline.Timeline
	Duration    timeline.TimeNS
	Kept        timeline.TimeNS
	Removed     timeline.TimeNS
	Cuts        int
	Reduction   float64
	VAAPIDevice string
}

func Analyze(ctx context.Context, cfg config.Config, status io.Writer) (Result, error) {
	info, err := media.Probe(cfg.Input)
	if err != nil {
		return Result{}, fmt.Errorf("probe %s: %w", cfg.Input, err)
	}
	if !info.Audio {
		return Result{}, fmt.Errorf("input has no audio stream to analyze")
	}
	if cfg.Threads <= 0 {
		cfg.Threads = analyze.WorkerCount(0)
	}
	phase, totalPhases := getWorkflow(ctx, 1, 2)
	reporter := NewProgressReporter(status, phase, totalPhases, "Analyzing Audio...")
	if reporter != nil {
		defer func() {
			if err != nil {
				reporter.Abort()
			}
		}()
	}
	analysisCtx, cancelAnalysis := context.WithCancel(ctx)
	defer cancelAnalysis()
	var modelHandle *audio.ModelHandle
	modelPath := ""
	if cfg.Noise.Enabled {
		handle, err := audio.OpenEmbeddedModel()
		if err != nil {
			return Result{}, err
		}
		modelHandle = handle
		defer modelHandle.Close()
		modelPath = modelHandle.Path()
	}
	decoder, err := media.OpenAudio(analysisCtx, cfg.Input, 1, cfg.Noise.Enabled,
		cfg.NoiseStrength(), modelPath)
	if err != nil {
		return Result{}, err
	}

	const blockSamples = 96_000
	blocks := make(chan analyze.PCMBlock, analyze.WorkerCount(cfg.Threads)*2)
	decodeErr := make(chan error, 1)
	decodeDone := make(chan struct{})
	go func() {
		defer close(decodeDone)
		defer close(blocks)
		defer close(decodeErr)

		splitter := newPCMBlockSplitter(blockSamples)
		readBuffer := make([]float32, 8192)

		emit := func(block analyze.PCMBlock) error {
			if err := analysisCtx.Err(); err != nil {
				return err
			}
			select {
			case blocks <- block:
				return nil
			case <-analysisCtx.Done():
				return analysisCtx.Err()
			}
		}

		for {
			count, start, readErr := decoder.Read(readBuffer)
			if readErr != nil {
				if analysisCtx.Err() != nil {
					decodeErr <- analysisCtx.Err()
					return
				}
				decodeErr <- readErr
				return
			}
			if count == 0 {
				if err := splitter.Flush(emit); err != nil {
					decodeErr <- err
					return
				}
				decodeErr <- nil
				return
			}
			if err := splitter.Push(readBuffer[:count], start, emit); err != nil {
				decodeErr <- err
				return
			}
			if reporter != nil && info.DurationNS > 0 {
				processed := start + int64(count)
				totalSamples := int64(timeline.SampleIndexAtOrAfter(info.DurationNS, cfg.AnalysisSampleRate()))
				pct := float64(processed) * 100.0 / float64(totalSamples)
				mediaDur := time.Duration(timeline.TimeForSamples(processed, cfg.AnalysisSampleRate()))
				reporter.Update(pct, mediaDur)
			}
		}
	}()
	defer func() {
		cancelAnalysis()
		<-decodeDone
		decoder.Close()
	}()
	detector, err := analyze.NewDetector(cfg.Silence, cfg.AnalysisSampleRate())
	if err != nil {
		return Result{}, err
	}
	audioSamples, err := analyze.AnalyzeBlocksTo(analysisCtx, blocks, cfg.AnalysisSampleRate(), cfg.Silence.WindowMS,
		cfg.Threads, detector.Consume)
	if err != nil {
		return Result{}, err
	}
	if decodeErrValue, ok := <-decodeErr; ok && decodeErrValue != nil {
		return Result{}, decodeErrValue
	}
	if reporter != nil {
		reporter.Finish()
	}
	cuts := detector.Finish(audioSamples)
	audioDuration := timeline.TimeForSamples(audioSamples, cfg.AnalysisSampleRate())
	sourceDuration := max(audioDuration, info.DurationNS)
	if sourceDuration <= 0 {
		return Result{}, fmt.Errorf("media duration is unavailable")
	}
	resultTimeline, err := timeline.FromCuts(sourceDuration, cuts)
	if err != nil {
		return Result{}, err
	}
	kept := resultTimeline.OutputDuration()
	removed := sourceDuration - kept
	reduction := 0.0
	if sourceDuration > 0 {
		reduction = float64(removed) * 100 / float64(sourceDuration)
	}
	return Result{Timeline: resultTimeline, Duration: sourceDuration, Kept: kept,
		Removed: removed, Cuts: resultTimeline.Cuts(), Reduction: reduction,
		VAAPIDevice: cfg.VAAPIDevice}, nil
}

func Render(ctx context.Context, cfg config.Config, result Result, status io.Writer) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := Preflight(cfg); err != nil {
		return err
	}
	if result.VAAPIDevice == "" {
		return fmt.Errorf("no usable VAAPI device selected")
	}
	_, totalPhases := getWorkflow(ctx, 2, 2)
	renderPhase := 2
	if totalPhases <= 1 {
		renderPhase = 1
	}
	reporter := NewProgressReporter(status, renderPhase, totalPhases, "Rendering Video...")
	if reporter != nil {
		cutEnds := make([]timeline.TimeNS, 0, len(result.Timeline.Segments()))
		for _, seg := range result.Timeline.Segments() {
			if seg.Action == timeline.Cut {
				cutEnds = append(cutEnds, seg.End)
			}
		}
		reporter.SetRenderMetrics(result.Cuts, cutEnds, time.Duration(result.Duration))
		defer func() {
			if err != nil {
				reporter.Abort()
			}
		}()
	}
	directory := filepath.Dir(cfg.Output)
	base := filepath.Base(cfg.Output)
	temporaryPath, err := createTemporaryOutput(directory, base)
	if err != nil {
		return fmt.Errorf("creating temporary output: %w", err)
	}
	defer os.Remove(temporaryPath)
	codec := string(cfg.VideoCodec)
	videoTag := ""
	if cfg.VideoCodec == config.VideoHEVC {
		videoTag = string(cfg.HEVCCodecTag)
	}
	var modelHandle *audio.ModelHandle
	modelPath := ""
	if cfg.Noise.Enabled {
		handle, err := audio.OpenEmbeddedModel()
		if err != nil {
			return err
		}
		modelHandle = handle
		defer modelHandle.Close()
		modelPath = modelHandle.Path()
	}
	err = media.Render(ctx, cfg.Input, temporaryPath, result.Timeline, media.RenderOptions{
		VideoCodec: codec, VideoTag: videoTag, VAAPIDevice: result.VAAPIDevice, OutputMono: cfg.Audio.Mono,
		RNNoiseEnabled: cfg.Noise.Enabled, RNNoiseStrength: cfg.NoiseStrength(), RNNoiseModel: modelPath,
		Threads: cfg.Threads,
	}, func(permille int) {
		if reporter != nil {
			pct := float64(permille) / 10.0
			mediaDur := time.Duration(float64(result.Duration) * (pct / 100.0))
			reporter.Update(pct, mediaDur)
		}
	})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := Publish(temporaryPath, cfg.Output, cfg.Force); err != nil {
		return err
	}
	if reporter != nil {
		reporter.Finish()
	}
	return nil
}

func createTemporaryOutput(directory, base string) (string, error) {
	temporary, err := os.CreateTemp(directory, "."+base+".lecturejet-*.mp4")
	if err != nil {
		return "", err
	}
	path := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := os.Chmod(path, 0o644); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}
