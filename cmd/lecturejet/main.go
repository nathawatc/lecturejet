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
			os.Exit(130)
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
	ctx = app.WithWorkflow(ctx, 1, 3)
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
	ctx = app.WithWorkflow(ctx, 1, 1)
	result, err := app.Analyze(ctx, cfg, stderr)
	if err != nil {
		return err
	}
	printResult(stdout, result)
	return nil
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
