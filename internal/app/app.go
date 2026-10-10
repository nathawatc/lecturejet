package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"codeberg.org/nathawat/lecturejet/internal/analyze"
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
	if status != nil {
		fmt.Fprintln(status, "Analyzing audio...")
	}
	analysisCtx, cancelAnalysis := context.WithCancel(ctx)
	defer cancelAnalysis()
	decoder, err := media.OpenAudio(analysisCtx, cfg.Input, 1, cfg.Noise.Enabled,
		cfg.NoiseStrength(), cfg.Noise.Model)
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
		lastBucket := -1

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
			if status != nil && info.DurationNS > 0 {
				percent := int((start + int64(count)) * 100 / int64(timeline.SampleIndexAtOrAfter(info.DurationNS, cfg.AnalysisSampleRate())))
				if percent > 100 {
					percent = 100
				}
				bucket := percent / 5
				if percent == 100 {
					bucket = 20
				}
				if bucket != lastBucket {
					fmt.Fprintf(status, "\rAnalyzing audio... %d%%  %s / %s", percent,
						timeline.TimeForSamples(start+int64(count), cfg.AnalysisSampleRate()),
						info.DurationNS)
					lastBucket = bucket
				}
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
	if status != nil {
		fmt.Fprintln(status)
	}
	cuts := detector.Finish(audioSamples)
	audioDuration := timeline.TimeForSamples(audioSamples, cfg.AnalysisSampleRate())
	sourceDuration := info.DurationNS
	if audioDuration > sourceDuration {
		sourceDuration = audioDuration
	}
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

func Render(ctx context.Context, cfg config.Config, result Result, status io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := Preflight(cfg); err != nil {
		return err
	}
	if result.VAAPIDevice == "" {
		return fmt.Errorf("no usable VAAPI device selected")
	}
	if status != nil {
		fmt.Fprintf(status, "Rendering %s VAAPI...", strings.ToUpper(string(cfg.VideoCodec)))
	}
	directory := filepath.Dir(cfg.Output)
	base := filepath.Base(cfg.Output)
	temporaryPath, err := createTemporaryOutput(directory, base)
	if err != nil {
		return fmt.Errorf("creating temporary output: %w", err)
	}
	defer os.Remove(temporaryPath)
	wantHVC1 := cfg.VideoCodec == config.VideoHEVC && cfg.HEVCCodecTag == config.HEVCCodecTagHVC1
	codec := media.VAAPIEncoderName(cfg.VideoCodec)
	videoTag := ""
	if cfg.VideoCodec == config.VideoHEVC {
		videoTag = string(config.HEVCCodecTagHEV1)
	}
	err = media.Render(ctx, cfg.Input, temporaryPath, result.Timeline, media.RenderOptions{
		VideoCodec: codec, VideoTag: videoTag, VAAPIDevice: result.VAAPIDevice, OutputMono: cfg.Audio.Mono,
		RNNoiseEnabled: cfg.Noise.Enabled, RNNoiseStrength: cfg.NoiseStrength(), RNNoiseModel: cfg.Noise.Model,
		Threads: cfg.Threads,
	}, func(percent int) {
		if status != nil {
			if wantHVC1 && percent > 95 {
				percent = 95
			}
			fmt.Fprintf(status, "\rRendering %s VAAPI... %d%%", strings.ToUpper(string(cfg.VideoCodec)), percent)
		}
	})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	finalPath := temporaryPath
	if wantHVC1 {
		remuxPath, remuxErr := createTemporaryOutput(directory, base)
		if remuxErr != nil {
			return fmt.Errorf("creating temporary remux output: %w", remuxErr)
		}
		defer os.Remove(remuxPath)
		if err := media.RemuxHEVCHVC1(ctx, temporaryPath, remuxPath); err != nil {
			return err
		}
		finalPath = remuxPath
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := Publish(finalPath, cfg.Output, cfg.Force); err != nil {
		return err
	}
	if status != nil {
		fmt.Fprintf(status, "\rRendering %s VAAPI... 100%%\n", strings.ToUpper(string(cfg.VideoCodec)))
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
	return path, nil
}
