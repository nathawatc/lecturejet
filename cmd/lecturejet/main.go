package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"codeberg.org/nathawat/lecturejet/internal/app"
	"codeberg.org/nathawat/lecturejet/internal/media"
	"codeberg.org/nathawat/lecturejet/internal/ui"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, context.Canceled) {
			if isTerminal(os.Stderr) {
				fmt.Fprint(os.Stderr, "\r\033[2K\033[?25h")
			}
			os.Exit(130)
		}
		if isTerminal(os.Stderr) {
			fmt.Fprint(os.Stderr, "\033[?25h")
		}
		fmt.Fprintf(os.Stderr, "lecturejet: %v\n", err)
		os.Exit(1)
	}
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
		return fmt.Errorf("usage: lecturejet input.mp4 [output.mp4] [options] or lecturejet analyze input.mp4")
	}
	if args[0] == "analyze" {
		return runAnalyze(args[1:], stdout, stderr)
	}
	cfg, interactive, codecExplicit, err := parseRenderArgsWithInput(stdin, args)
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
	media.SetVerbose(cfg.Verbose)
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
	ctx = app.WithWorkflow(ctx, 1, 2)
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
	media.SetVerbose(cfg.Verbose)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx = app.WithWorkflow(ctx, 1, 1)
	result, err := app.Analyze(ctx, cfg, stderr)
	if err != nil {
		return err
	}
	printResult(stdout, result)
	return nil
}

func isTerminal(v any) bool {
	if file, ok := v.(*os.File); ok && file != nil {
		info, err := file.Stat()
		return err == nil && info.Mode()&os.ModeCharDevice != 0
	}
	return false
}

func printResult(out io.Writer, result app.Result) {
	dur := float64(result.Duration)
	var keptPct, remPct float64
	if dur > 0 {
		keptPct = float64(result.Kept) * 100.0 / dur
		remPct = float64(result.Removed) * 100.0 / dur
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "┌─ Silence Cut Analysis ──────────────────────────────────┐")
	line1 := fmt.Sprintf("│  Original:  %s", result.Duration)
	fmt.Fprintf(out, "%-58s│\n", line1)
	line2 := fmt.Sprintf("│  Kept:      %s (%.2f%%)", result.Kept, keptPct)
	fmt.Fprintf(out, "%-58s│\n", line2)
	line3 := fmt.Sprintf("│  Removed:   %s (%.2f%%)", result.Removed, remPct)
	fmt.Fprintf(out, "%-58s│\n", line3)
	fmt.Fprintln(out, "└─────────────────────────────────────────────────────────┘")
	fmt.Fprintln(out)
}
