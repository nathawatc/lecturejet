package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"lecturejet/internal/app"
	"lecturejet/internal/config"
	"lecturejet/internal/media"
	"lecturejet/internal/ui"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, context.Canceled) {
			os.Exit(130)
		}
		fmt.Fprintf(os.Stderr, "lecturejet: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: lecturejet input.mp4 output.mp4 [options] or lecturejet analyze input.mp4")
	}
	if args[0] == "analyze" {
		return runAnalyze(args[1:], stdout, stderr)
	}
	cfg, interactive, codecExplicit, err := parseRenderArgs(args)
	if err != nil {
		return err
	}
	deviceExplicit := cfg.VAAPIDevice != ""
	info, err := media.Probe(cfg.Input)
	if err != nil {
		return fmt.Errorf("probe %s: %w", cfg.Input, err)
	}
	choices := app.ProbeVAAPIChoices(info, cfg.VideoCodec, cfg.VAAPIDevice)
	if interactive {
		if codecExplicit {
			selectedChoices := choices[:0]
			for _, choice := range choices {
				if choice.Codec == cfg.VideoCodec {
					selectedChoices = append(selectedChoices, choice)
				}
			}
			choices = selectedChoices
		}
		if len(choices) == 0 {
			if codecExplicit {
				return fmt.Errorf("selected %s VAAPI encoder is unavailable for this input/device", cfg.VideoCodec)
			}
			return fmt.Errorf("no usable VAAPI encoder for %dx%d on selected device", info.VideoWidth, info.VideoHeight)
		}
		cfg, err = ui.Configure(stdin, stderr, cfg, choices)
		if err != nil {
			return err
		}
	} else {
		cfg, err = selectVAAPIConfig(cfg, choices, codecExplicit, deviceExplicit, info.VideoWidth, info.VideoHeight)
		if err != nil {
			return err
		}
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if !info.Video {
		return fmt.Errorf("input has no video stream")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	result, err := app.Analyze(ctx, cfg, stderr)
	if err != nil {
		return err
	}
	printResult(stdout, result)
	if err := app.Render(ctx, cfg, result, stderr); err != nil {
		return err
	}
	return nil
}

func selectVAAPIConfig(
	cfg config.Config,
	choices []ui.VAAPIChoice,
	codecExplicit bool,
	deviceExplicit bool,
	width int,
	height int,
) (config.Config, error) {
	var selected ui.VAAPIChoice
	found := false
	for _, choice := range choices {
		if deviceExplicit && choice.Device != cfg.VAAPIDevice {
			continue
		}
		if codecExplicit && choice.Codec != cfg.VideoCodec {
			continue
		}
		selected = choice
		found = true
		break
	}
	if !found {
		if codecExplicit {
			return config.Config{}, fmt.Errorf("selected %s VAAPI encoder is unavailable for this input/device", cfg.VideoCodec)
		}
		return config.Config{}, fmt.Errorf("no usable VAAPI encoder for %dx%d on selected device", width, height)
	}

	if !codecExplicit {
		cfg.VideoCodec = selected.Codec
	}
	if !deviceExplicit {
		cfg.VAAPIDevice = selected.Device
	}
	return cfg, nil
}

func runAnalyze(args []string, stdout, stderr io.Writer) error {
	cfg, err := parseAnalyzeArgs(args)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	result, err := app.Analyze(ctx, cfg, stderr)
	if err != nil {
		return err
	}
	printResult(stdout, result)
	return nil
}

func parseRenderArgs(args []string) (config.Config, bool, bool, error) {
	if len(args) < 2 {
		return config.Config{}, false, false, fmt.Errorf("usage: lecturejet input.mp4 output.mp4 [options]")
	}
	cfg := config.Default()
	cfg.Input, cfg.Output = args[0], args[1]
	flags := flag.NewFlagSet("lecturejet", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	threads := flags.String("threads", "auto", "analysis workers or auto")
	codec := flags.String("video-codec", string(cfg.VideoCodec), "hevc or h264")
	hevcTag := flags.String("hevc-tag", string(cfg.HEVCCodecTag), "HEVC codec tag: hev1 or hvc1")
	device := flags.String("vaapi-device", "", "VAAPI render device")
	rnnoise := flags.Bool("rnnoise", false, "enable RNNoise")
	strength := flags.Int("rnnoise-strength", cfg.Noise.Strength, "RNNoise strength 0-100")
	model := flags.String("rnnoise-model", "", "RNNoise .rnnn model")
	mono := flags.Bool("mono", false, "convert output audio to mono")
	stereo := flags.Bool("stereo", false, "preserve stereo output")
	threshold := flags.Float64("threshold", cfg.Silence.ThresholdDB, "silence threshold dBFS")
	minSilence := flags.Int("min-silence", cfg.Silence.MinSilenceMS, "minimum silence milliseconds")
	margin := flags.Int("margin", cfg.Silence.MarginMS, "speech protection margin milliseconds")
	nonInteractive := flags.Bool("non-interactive", false, "use defaults without wizard")
	if err := flags.Parse(args[2:]); err != nil {
		return config.Config{}, false, false, err
	}
	if flags.NArg() != 0 {
		return config.Config{}, false, false, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if *mono && *stereo {
		return config.Config{}, false, false, fmt.Errorf("--mono and --stereo conflict")
	}
	cfg.VideoCodec = config.VideoCodec(strings.ToLower(*codec))
	cfg.HEVCCodecTag = config.HEVCCodecTag(strings.ToLower(*hevcTag))
	cfg.VAAPIDevice = *device
	cfg.Noise.Enabled = *rnnoise
	cfg.Noise.Strength = *strength
	cfg.Noise.Model = *model
	if *mono {
		cfg.Audio.Mono = true
	}
	if *stereo {
		cfg.Audio.Mono = false
	}
	cfg.Silence.ThresholdDB = *threshold
	cfg.Silence.MinSilenceMS = *minSilence
	cfg.Silence.MarginMS = *margin
	if *threads == "auto" {
		cfg.Threads = 0
	} else {
		value, err := strconv.Atoi(*threads)
		if err != nil || value < 1 {
			return config.Config{}, false, false, fmt.Errorf("invalid --threads %q", *threads)
		}
		cfg.Threads = value
	}
	interactive := !*nonInteractive && isTerminal(os.Stdin)
	codecExplicit := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "video-codec" {
			codecExplicit = true
		}
	})
	return cfg, interactive, codecExplicit, nil
}

func parseAnalyzeArgs(args []string) (config.Config, error) {
	if len(args) < 1 {
		return config.Config{}, fmt.Errorf("usage: lecturejet analyze input.mp4 [options]")
	}
	cfg := config.Default()
	cfg.Input = args[0]
	cfg.Output = "analysis"
	flags := flag.NewFlagSet("lecturejet analyze", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	threads := flags.String("threads", "auto", "analysis workers or auto")
	rnnoise := flags.Bool("rnnoise", false, "enable RNNoise")
	strength := flags.Int("rnnoise-strength", cfg.Noise.Strength, "RNNoise strength 0-100")
	model := flags.String("rnnoise-model", "", "RNNoise .rnnn model")
	flags.Float64Var(&cfg.Silence.ThresholdDB, "threshold", cfg.Silence.ThresholdDB, "silence threshold dBFS")
	flags.IntVar(&cfg.Silence.MinSilenceMS, "min-silence", cfg.Silence.MinSilenceMS, "minimum silence milliseconds")
	flags.IntVar(&cfg.Silence.MarginMS, "margin", cfg.Silence.MarginMS, "speech protection margin milliseconds")
	if err := flags.Parse(args[1:]); err != nil {
		return config.Config{}, err
	}
	if flags.NArg() != 0 {
		return config.Config{}, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	cfg.Noise.Enabled, cfg.Noise.Strength, cfg.Noise.Model = *rnnoise, *strength, *model
	if *threads == "auto" {
		cfg.Threads = 0
	} else {
		value, err := strconv.Atoi(*threads)
		if err != nil || value < 1 {
			return config.Config{}, fmt.Errorf("invalid --threads %q", *threads)
		}
		cfg.Threads = value
	}
	return cfg, nil
}

func isTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func printResult(out io.Writer, result app.Result) {
	fmt.Fprintf(out, "Duration       %s\n", app.FormatDuration(result.Duration))
	fmt.Fprintf(out, "Kept           %s\n", app.FormatDuration(result.Kept))
	fmt.Fprintf(out, "Removed        %s\n", app.FormatDuration(result.Removed))
	fmt.Fprintf(out, "Cuts           %d\n", result.Cuts)
	fmt.Fprintf(out, "Reduction      %.2f%%\n", result.Reduction)
}
