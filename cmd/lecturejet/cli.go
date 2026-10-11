package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"

	"codeberg.org/nathawat/lecturejet/internal/config"
)

var versionOverride string

func isHelpFlag(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "-help"
}

func hasHelpFlag(args []string) bool {
	return slices.ContainsFunc(args, isHelpFlag)
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

type analysisFlagValues struct {
	threads    *string
	verbose    *bool
	rnnoise    *bool
	strength   *int
	threshold  *float64
	minSilence *int
	margin     *int
}

func registerAnalysisFlags(flags *flag.FlagSet, cfg config.Config) *analysisFlagValues {
	af := &analysisFlagValues{
		threads:    flags.String("threads", "auto", "analysis workers or auto"),
		verbose:    flags.Bool("verbose", false, "verbose logging"),
		rnnoise:    flags.Bool("rnnoise", false, "enable RNNoise"),
		strength:   flags.Int("rnnoise-strength", cfg.Noise.Strength, "RNNoise strength 0-100"),
		threshold:  flags.Float64("threshold", cfg.Silence.ThresholdDB, "silence threshold dBFS"),
		minSilence: flags.Int("min-silence", cfg.Silence.MinSilenceMS, "minimum silence milliseconds"),
		margin:     flags.Int("margin", cfg.Silence.MarginMS, "speech protection margin milliseconds"),
	}
	flags.BoolVar(af.verbose, "v", false, "verbose logging")
	return af
}

func applyAnalysisFlags(cfg *config.Config, af *analysisFlagValues) error {
	cfg.Verbose = *af.verbose
	cfg.Noise.Enabled = *af.rnnoise
	cfg.Noise.Strength = *af.strength
	cfg.Silence.ThresholdDB = *af.threshold
	cfg.Silence.MinSilenceMS = *af.minSilence
	cfg.Silence.MarginMS = *af.margin
	if *af.threads == "auto" {
		cfg.Threads = 0
	} else {
		value, err := strconv.Atoi(*af.threads)
		if err != nil || value < 1 {
			return fmt.Errorf("invalid --threads %q", *af.threads)
		}
		cfg.Threads = value
	}
	return nil
}

type renderFlagValues struct {
	analysisFlagValues
	force          *bool
	codec          *string
	hevcTag        *string
	device         *string
	mono           *bool
	stereo         *bool
	nonInteractive *bool
}

func newRenderFlagSet(cfg config.Config, out io.Writer) (*flag.FlagSet, *renderFlagValues) {
	flags := flag.NewFlagSet("lecturejet", flag.ContinueOnError)
	flags.SetOutput(out)
	af := registerAnalysisFlags(flags, cfg)
	rf := &renderFlagValues{
		analysisFlagValues: *af,
		force:              flags.Bool("force", false, "allow replacing an existing output file"),
		codec:              flags.String("video-codec", string(cfg.VideoCodec), "hevc or h264"),
		hevcTag:            flags.String("hevc-tag", string(cfg.HEVCCodecTag), "HEVC codec tag: hev1 or hvc1"),
		device:             flags.String("vaapi-device", "", "VAAPI render device"),
		mono:               flags.Bool("mono", false, "convert output audio to mono"),
		stereo:             flags.Bool("stereo", false, "preserve stereo output"),
		nonInteractive:     flags.Bool("non-interactive", false, "use defaults without wizard"),
	}
	return flags, rf
}

func newAnalyzeFlagSet(cfg config.Config, out io.Writer) (*flag.FlagSet, *analysisFlagValues) {
	flags := flag.NewFlagSet("lecturejet analyze", flag.ContinueOnError)
	flags.SetOutput(out)
	af := registerAnalysisFlags(flags, cfg)
	return flags, af
}

func defaultOutput(input string) string {
	ext := filepath.Ext(input)
	if ext == "" {
		return input + ".out"
	}
	return strings.TrimSuffix(input, ext) + ".out" + ext
}

func printTopLevelHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  lecturejet <input.mp4> [output.mp4] [options]")
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
	fmt.Fprintln(out, "  lecturejet <input.mp4> [output.mp4] [options]")
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

func parseRenderArgs(args []string) (config.Config, bool, bool, error) {
	return parseRenderArgsWithInput(os.Stdin, args)
}

func parseRenderArgsWithInput(in io.Reader, args []string) (config.Config, bool, bool, error) {
	if len(args) < 1 || strings.HasPrefix(args[0], "-") {
		if hasHelpFlag(args) {
			return config.Config{}, false, false, flag.ErrHelp
		}
		return config.Config{}, false, false, fmt.Errorf("usage: lecturejet input.mp4 [output.mp4] [options]")
	}
	cfg := config.Default()
	cfg.Input = args[0]

	var flagArgs []string
	autoOutput := false
	if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
		cfg.Output = args[1]
		flagArgs = args[2:]
	} else {
		autoOutput = true
		flagArgs = args[1:]
	}

	flags, rf := newRenderFlagSet(cfg, io.Discard)
	if err := flags.Parse(flagArgs); err != nil {
		return config.Config{}, false, false, err
	}
	if flags.NArg() != 0 {
		return config.Config{}, false, false, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if *rf.mono && *rf.stereo {
		return config.Config{}, false, false, fmt.Errorf("--mono and --stereo conflict")
	}
	cfg.Force = *rf.force
	if autoOutput {
		if cfg.Force {
			cfg.Output = cfg.Input
		} else {
			cfg.Output = defaultOutput(cfg.Input)
		}
	}
	if err := applyAnalysisFlags(&cfg, &rf.analysisFlagValues); err != nil {
		return config.Config{}, false, false, err
	}
	cfg.VideoCodec = config.VideoCodec(strings.ToLower(*rf.codec))
	cfg.HEVCCodecTag = config.HEVCCodecTag(strings.ToLower(*rf.hevcTag))
	cfg.VAAPIDevice = *rf.device
	if *rf.mono {
		cfg.Audio.Mono = true
	}
	if *rf.stereo {
		cfg.Audio.Mono = false
	}
	interactive := !*rf.nonInteractive && isTerminal(in)
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
	if err := applyAnalysisFlags(&cfg, af); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}
