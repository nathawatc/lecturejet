package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"

	"codeberg.org/nathawat/lecturejet/internal/app"
	"codeberg.org/nathawat/lecturejet/internal/config"
	"codeberg.org/nathawat/lecturejet/internal/media"
	"codeberg.org/nathawat/lecturejet/internal/ui"
)

var versionOverride string

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, context.Canceled) {
			os.Exit(130)
		}
		fmt.Fprintf(os.Stderr, "lecturejet: %v\n", err)
		os.Exit(1)
	}
}

func isHelpFlag(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "-help"
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if isHelpFlag(arg) {
			return true
		}
	}
	return false
}

func isTopLevelHelp(args []string) bool {
	return len(args) > 0 && isHelpFlag(args[0])
}

func isVersion(args []string) bool {
	return len(args) == 1 && (args[0] == "--version" || args[0] == "-version")
}

func formatVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "(devel)" {
		return "dev"
	}
	return strings.TrimPrefix(v, "v")
}

func versionFromBuildInfo(info *debug.BuildInfo) string {
	if info != nil && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return formatVersion(info.Main.Version)
	}
	return "dev"
}

func currentVersion() string {
	if versionOverride != "" {
		return formatVersion(versionOverride)
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	return versionFromBuildInfo(info)
}

func printVersion(out io.Writer) {
	fmt.Fprintf(out, "lecturejet %s\n", currentVersion())
}

type renderFlagValues struct {
	force          *bool
	threads        *string
	codec          *string
	hevcTag        *string
	device         *string
	rnnoise        *bool
	strength       *int
	model          *string
	mono           *bool
	stereo         *bool
	threshold      *float64
	minSilence     *int
	margin         *int
	nonInteractive *bool
}

func newRenderFlagSet(cfg config.Config, out io.Writer) (*flag.FlagSet, *renderFlagValues) {
	flags := flag.NewFlagSet("lecturejet", flag.ContinueOnError)
	flags.SetOutput(out)
	rf := &renderFlagValues{
		force:          flags.Bool("force", false, "allow replacing an existing output file"),
		threads:        flags.String("threads", "auto", "analysis workers or auto"),
		codec:          flags.String("video-codec", string(cfg.VideoCodec), "hevc or h264"),
		hevcTag:        flags.String("hevc-tag", string(cfg.HEVCCodecTag), "HEVC codec tag: hev1 or hvc1"),
		device:         flags.String("vaapi-device", "", "VAAPI render device"),
		rnnoise:        flags.Bool("rnnoise", false, "enable RNNoise"),
		strength:       flags.Int("rnnoise-strength", cfg.Noise.Strength, "RNNoise strength 0-100"),
		model:          flags.String("rnnoise-model", "", "RNNoise .rnnn model"),
		mono:           flags.Bool("mono", false, "convert output audio to mono"),
		stereo:         flags.Bool("stereo", false, "preserve stereo output"),
		threshold:      flags.Float64("threshold", cfg.Silence.ThresholdDB, "silence threshold dBFS"),
		minSilence:     flags.Int("min-silence", cfg.Silence.MinSilenceMS, "minimum silence milliseconds"),
		margin:         flags.Int("margin", cfg.Silence.MarginMS, "speech protection margin milliseconds"),
		nonInteractive: flags.Bool("non-interactive", false, "use defaults without wizard"),
	}
	return flags, rf
}

type analyzeFlagValues struct {
	threads    *string
	rnnoise    *bool
	strength   *int
	model      *string
	threshold  *float64
	minSilence *int
	margin     *int
}

func newAnalyzeFlagSet(cfg config.Config, out io.Writer) (*flag.FlagSet, *analyzeFlagValues) {
	flags := flag.NewFlagSet("lecturejet analyze", flag.ContinueOnError)
	flags.SetOutput(out)
	af := &analyzeFlagValues{
		threads:    flags.String("threads", "auto", "analysis workers or auto"),
		rnnoise:    flags.Bool("rnnoise", false, "enable RNNoise"),
		strength:   flags.Int("rnnoise-strength", cfg.Noise.Strength, "RNNoise strength 0-100"),
		model:      flags.String("rnnoise-model", "", "RNNoise .rnnn model"),
		threshold:  flags.Float64("threshold", cfg.Silence.ThresholdDB, "silence threshold dBFS"),
		minSilence: flags.Int("min-silence", cfg.Silence.MinSilenceMS, "minimum silence milliseconds"),
		margin:     flags.Int("margin", cfg.Silence.MarginMS, "speech protection margin milliseconds"),
	}
	return flags, af
}

func printTopLevelHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  lecturejet <input.mp4> <output.mp4> [options]")
	fmt.Fprintln(out, "  lecturejet analyze <input.mp4> [options]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Commands:")
	fmt.Fprintln(out, "  analyze    Analyze silence without rendering")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Render options:")
	flags, _ := newRenderFlagSet(config.Default(), out)
	flags.PrintDefaults()
	fmt.Fprintln(out)
	fmt.Fprintln(out, `Use "lecturejet analyze --help" for analysis options.`)
}

func printRenderHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  lecturejet <input.mp4> <output.mp4> [options]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	flags, _ := newRenderFlagSet(config.Default(), out)
	flags.PrintDefaults()
}

func printAnalyzeHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  lecturejet analyze <input.mp4> [options]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	flags, _ := newAnalyzeFlagSet(config.Default(), out)
	flags.PrintDefaults()
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if isTopLevelHelp(args) {
		printTopLevelHelp(stdout)
		return nil
	}
	if isVersion(args) {
		printVersion(stdout)
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: lecturejet input.mp4 output.mp4 [options] or lecturejet analyze input.mp4")
	}
	if args[0] == "analyze" {
		return runAnalyze(args[1:], stdout, stderr)
	}
	cfg, interactive, codecExplicit, err := parseRenderArgs(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printRenderHelp(stdout)
			return nil
		}
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := app.Preflight(cfg); err != nil {
		return err
	}
	deviceExplicit := cfg.VAAPIDevice != ""
	info, err := media.Probe(cfg.Input)
	if err != nil {
		return fmt.Errorf("probe %s: %w", cfg.Input, err)
	}
	if !info.Video {
		return fmt.Errorf("input has no video stream")
	}
	choices := ui.ProbeVAAPIChoices(info, cfg.VideoCodec, cfg.VAAPIDevice)
	if interactive {
		if codecExplicit {
			choices = ui.FilterChoices(choices, cfg.VideoCodec)
		}
		if err := ui.CheckAvailable(choices, cfg.VideoCodec, info.VideoWidth, info.VideoHeight, codecExplicit, deviceExplicit); err != nil {
			return err
		}
		cfg, err = ui.Configure(stdin, stderr, cfg, choices)
		if err != nil {
			return err
		}
	} else {
		cfg, err = ui.SelectVAAPI(cfg, choices, codecExplicit, deviceExplicit, info.VideoWidth, info.VideoHeight)
		if err != nil {
			return err
		}
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
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

func runAnalyze(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: lecturejet analyze input.mp4 [options]")
	}
	if isHelpFlag(args[0]) {
		printAnalyzeHelp(stdout)
		return nil
	}
	cfg, err := parseAnalyzeArgs(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printAnalyzeHelp(stdout)
			return nil
		}
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
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
		if hasHelpFlag(args) {
			return config.Config{}, false, false, flag.ErrHelp
		}
		return config.Config{}, false, false, fmt.Errorf("usage: lecturejet input.mp4 output.mp4 [options]")
	}
	if isHelpFlag(args[1]) {
		return config.Config{}, false, false, flag.ErrHelp
	}
	cfg := config.Default()
	cfg.Input, cfg.Output = args[0], args[1]
	flags, rf := newRenderFlagSet(cfg, io.Discard)
	if err := flags.Parse(args[2:]); err != nil {
		return config.Config{}, false, false, err
	}
	if flags.NArg() != 0 {
		return config.Config{}, false, false, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if *rf.mono && *rf.stereo {
		return config.Config{}, false, false, fmt.Errorf("--mono and --stereo conflict")
	}
	cfg.Force = *rf.force
	cfg.VideoCodec = config.VideoCodec(strings.ToLower(*rf.codec))
	cfg.HEVCCodecTag = config.HEVCCodecTag(strings.ToLower(*rf.hevcTag))
	cfg.VAAPIDevice = *rf.device
	cfg.Noise.Enabled = *rf.rnnoise
	cfg.Noise.Strength = *rf.strength
	cfg.Noise.Model = *rf.model
	if *rf.mono {
		cfg.Audio.Mono = true
	}
	if *rf.stereo {
		cfg.Audio.Mono = false
	}
	cfg.Silence.ThresholdDB = *rf.threshold
	cfg.Silence.MinSilenceMS = *rf.minSilence
	cfg.Silence.MarginMS = *rf.margin
	if *rf.threads == "auto" {
		cfg.Threads = 0
	} else {
		value, err := strconv.Atoi(*rf.threads)
		if err != nil || value < 1 {
			return config.Config{}, false, false, fmt.Errorf("invalid --threads %q", *rf.threads)
		}
		cfg.Threads = value
	}
	interactive := !*rf.nonInteractive && isTerminal(os.Stdin)
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
	if isHelpFlag(args[0]) {
		return config.Config{}, flag.ErrHelp
	}
	cfg := config.Default()
	cfg.Input = args[0]
	cfg.Output = "analysis.mp4"
	flags, af := newAnalyzeFlagSet(cfg, io.Discard)
	if err := flags.Parse(args[1:]); err != nil {
		return config.Config{}, err
	}
	if flags.NArg() != 0 {
		return config.Config{}, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	cfg.Noise.Enabled, cfg.Noise.Strength, cfg.Noise.Model = *af.rnnoise, *af.strength, *af.model
	cfg.Silence.ThresholdDB = *af.threshold
	cfg.Silence.MinSilenceMS = *af.minSilence
	cfg.Silence.MarginMS = *af.margin
	if *af.threads == "auto" {
		cfg.Threads = 0
	} else {
		value, err := strconv.Atoi(*af.threads)
		if err != nil || value < 1 {
			return config.Config{}, fmt.Errorf("invalid --threads %q", *af.threads)
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
	fmt.Fprintf(out, "Duration       %s\n", result.Duration)
	fmt.Fprintf(out, "Kept           %s\n", result.Kept)
	fmt.Fprintf(out, "Removed        %s\n", result.Removed)
	fmt.Fprintf(out, "Cuts           %d\n", result.Cuts)
	fmt.Fprintf(out, "Reduction      %.2f%%\n", result.Reduction)
}
