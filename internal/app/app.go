package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"lecturejet/internal/analyze"
	"lecturejet/internal/config"
	"lecturejet/internal/media"
	"lecturejet/internal/timeline"
	"lecturejet/internal/ui"
)

type Result struct {
	Timeline     timeline.Timeline
	Duration     timeline.TimeNS
	Kept         timeline.TimeNS
	Removed      timeline.TimeNS
	Cuts         int
	Reduction    float64
	AudioSamples int64
	WorkerCount  int
	VAAPIDevice  string
	VideoCodec   config.VideoCodec
}

func ProbeVAAPIChoices(info media.ProbeInfo, preferred config.VideoCodec, requestedDevice string) []ui.VAAPIChoice {
	devices := []string{}
	if requestedDevice != "" {
		devices = append(devices, requestedDevice)
	} else {
		matches, _ := filepath.Glob("/dev/dri/renderD*")
		sort.Strings(matches)
		devices = matches
	}
	codecs := []config.VideoCodec{preferred}
	if preferred == config.VideoHEVC {
		codecs = append(codecs, config.VideoH264)
	} else {
		codecs = append(codecs, config.VideoHEVC)
	}
	choices := make([]ui.VAAPIChoice, 0, len(codecs))
	for _, codec := range codecs {
		encoder := "h264_vaapi"
		if codec == config.VideoHEVC {
			encoder = "hevc_vaapi"
		}
		for _, device := range devices {
			if err := media.ProbeVAAPI(device, encoder, info.VideoWidth, info.VideoHeight); err == nil {
				choices = append(choices, ui.VAAPIChoice{Codec: codec, Device: device})
				break
			}
		}
	}
	return choices
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
	decoder, err := media.OpenAudioWithContext(analysisCtx, cfg.Input, 1, cfg.Noise.Enabled,
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
						FormatDuration(timeline.TimeForSamples(start+int64(count), cfg.AnalysisSampleRate())),
						FormatDuration(info.DurationNS))
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
	detectorConfig := analyze.SilenceConfig{
		WindowMS: cfg.Silence.WindowMS, ThresholdDB: cfg.Silence.ThresholdDB,
		MinSilenceMS: cfg.Silence.MinSilenceMS, MarginMS: cfg.Silence.MarginMS,
		HysteresisDB: cfg.Silence.HysteresisDB,
	}
	detector, err := analyze.NewDetector(detectorConfig, cfg.AnalysisSampleRate())
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
		AudioSamples: audioSamples, WorkerCount: analyze.WorkerCount(cfg.Threads),
		VAAPIDevice: cfg.VAAPIDevice, VideoCodec: cfg.VideoCodec}, nil
}

func Render(ctx context.Context, cfg config.Config, result Result, status io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	inputPath, inputErr := filepath.Abs(cfg.Input)
	outputPath, outputErr := filepath.Abs(cfg.Output)
	if inputErr == nil && outputErr == nil && inputPath == outputPath {
		return fmt.Errorf("input and output paths must differ")
	}
	if result.VAAPIDevice == "" {
		return fmt.Errorf("no usable VAAPI device selected")
	}
	if status != nil {
		fmt.Fprintf(status, "Rendering %s VAAPI... 0%%\n", strings.ToUpper(string(cfg.VideoCodec)))
	}
	directory := filepath.Dir(cfg.Output)
	base := filepath.Base(cfg.Output)
	temporaryPath, err := createTemporaryOutput(directory, base)
	if err != nil {
		return fmt.Errorf("creating temporary output: %w", err)
	}
	defer os.Remove(temporaryPath)
	wantHVC1 := cfg.VideoCodec == config.VideoHEVC && cfg.HEVCCodecTag == config.HEVCCodecTagHVC1
	codec := "h264_vaapi"
	if cfg.VideoCodec == config.VideoHEVC {
		codec = "hevc_vaapi"
	}
	videoTag := ""
	if cfg.VideoCodec == config.VideoHEVC {
		videoTag = string(config.HEVCCodecTagHEV1)
	}
	err = media.RenderWithProgressContext(ctx, cfg.Input, temporaryPath, result.Timeline, media.RenderOptions{
		VideoCodec: codec, VideoTag: videoTag, VAAPIDevice: result.VAAPIDevice, OutputMono: cfg.Audio.Mono,
		RNNoiseEnabled: cfg.Noise.Enabled, RNNoiseStrength: cfg.NoiseStrength(), RNNoiseModel: cfg.Noise.Model,
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
		if err := media.RemuxHEVCHVC1WithContext(ctx, temporaryPath, remuxPath); err != nil {
			return err
		}
		finalPath = remuxPath
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(finalPath, cfg.Output); err != nil {
		return fmt.Errorf("publishing output: %w", err)
	}
	if status != nil {
		fmt.Fprintf(status, "Rendering %s VAAPI... 100%%\n", strings.ToUpper(string(cfg.VideoCodec)))
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

func FormatDuration(value timeline.TimeNS) string {
	if value < 0 {
		value = 0
	}
	totalMS := int64(value) / 1_000_000
	ms := totalMS % 1000
	totalSeconds := totalMS / 1000
	seconds := totalSeconds % 60
	totalMinutes := totalSeconds / 60
	minutes := totalMinutes % 60
	hours := totalMinutes / 60
	return fmt.Sprintf("%02d:%02d:%02d.%03d", hours, minutes, seconds, ms)
}

func RuntimeSummary() string {
	return fmt.Sprintf("Go %s, %d logical CPUs", runtime.Version(), runtime.NumCPU())
}
