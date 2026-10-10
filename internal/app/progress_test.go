package app

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"codeberg.org/nathawat/lecturejet/internal/timeline"
)

func TestProgressReporterTTY(t *testing.T) {
	var buf bytes.Buffer
	reporter := newProgressReporterWithTTY(&buf, 2, 2, "Rendering Video...", true)
	reporter.SetRenderMetrics(100, []timeline.TimeNS{10 * 1_000_000_000, 20 * 1_000_000_000}, 30*time.Second)

	reporter.Update(78.4, 25*time.Second)
	output := buf.String()

	if !strings.Contains(output, "\033[?25l") {
		t.Errorf("expected cursor hide code, got %q", output)
	}
	if !strings.Contains(output, "\033]9;4;1;") {
		t.Errorf("expected OSC 9;4 in-progress escape sequence, got %q", output)
	}
	if !strings.Contains(output, "\r\033[2K") {
		t.Errorf("expected clear-line escape code, got %q", output)
	}
	if !strings.Contains(output, "[2/2] Rendering Video...") {
		t.Errorf("expected stage indicator, got %q", output)
	}
	if !strings.Contains(output, "78.4%") {
		t.Errorf("expected percentage 78.4%%, got %q", output)
	}
	if !strings.Contains(output, "━") {
		t.Errorf("expected progress bar blocks, got %q", output)
	}
	if !strings.Contains(output, "Elapsed:") || !strings.Contains(output, "ETA:") {
		t.Errorf("expected Elapsed and ETA metrics, got %q", output)
	}
	if !strings.Contains(output, "cuts passed") {
		t.Errorf("expected cuts passed counter, got %q", output)
	}

	buf.Reset()
	reporter.Finish()
	finishOutput := buf.String()
	if !strings.Contains(finishOutput, "\033]9;4;0;0\033\\") {
		t.Errorf("expected OSC 9;4 completed sequence on Finish, got %q", finishOutput)
	}
	if !strings.Contains(finishOutput, "\033[?25h") {
		t.Errorf("expected cursor restore code on Finish, got %q", finishOutput)
	}
	if !strings.HasSuffix(finishOutput, "\n") {
		t.Errorf("expected trailing newline on Finish, got %q", finishOutput)
	}
}

func TestProgressReporterNonTTY(t *testing.T) {
	var buf bytes.Buffer
	reporter := newProgressReporterWithTTY(&buf, 1, 2, "Analyzing Audio...", false)

	reporter.Update(50.0, 10*time.Second)
	output := buf.String()

	if strings.Contains(output, "\033]") {
		t.Errorf("expected no OSC escape sequences in non-TTY mode, got %q", output)
	}
	if strings.Contains(output, "\033[2K") {
		t.Errorf("expected no clear-line escape code in non-TTY mode, got %q", output)
	}
	if strings.Contains(output, "\033[?25") {
		t.Errorf("expected no cursor escape codes in non-TTY mode, got %q", output)
	}
	if !strings.Contains(output, "[1/2] Analyzing Audio...") {
		t.Errorf("expected stage name in output, got %q", output)
	}
	if !strings.Contains(output, "50.0%") {
		t.Errorf("expected 50.0%%, got %q", output)
	}

	buf.Reset()
	reporter.Abort()
	abortOutput := buf.String()
	if strings.Contains(abortOutput, "\033]") {
		t.Errorf("expected no OSC escape sequences in non-TTY abort, got %q", abortOutput)
	}
	if !strings.HasSuffix(abortOutput, "\n") {
		t.Errorf("expected trailing newline on Abort, got %q", abortOutput)
	}
}

func TestProgressReporterMonotonicity(t *testing.T) {
	var buf bytes.Buffer
	reporter := newProgressReporterWithTTY(&buf, 2, 2, "Rendering Video...", false)

	reporter.Update(40.0, 0)
	buf.Reset()

	// Progress attempts to jump backwards to 30.0% (e.g. seek recovery)
	reporter.Update(30.0, 0)
	if reporter.lastPct < 40.0 {
		t.Errorf("lastPct decreased to %f, want >= 40.0", reporter.lastPct)
	}
}

func TestProgressReporterThrottling(t *testing.T) {
	var buf bytes.Buffer
	reporter := newProgressReporterWithTTY(&buf, 1, 1, "Analyzing Audio...", false)

	reporter.Update(10.0, 0)
	buf.Reset()

	// Update with only +0.05% immediately afterwards should be throttled
	reporter.Update(10.05, 0)
	if buf.Len() > 0 {
		t.Errorf("expected update to be throttled, got %q", buf.String())
	}

	// Update with >= +5.0% in non-TTY should bypass throttling
	reporter.Update(15.5, 0)
	if buf.Len() == 0 {
		t.Errorf("expected update >= 5.0%% to be rendered in non-TTY, got nothing")
	}
}

func TestProgressReporterAbortTTY(t *testing.T) {
	var buf bytes.Buffer
	reporter := newProgressReporterWithTTY(&buf, 2, 2, "Rendering Video...", true)

	reporter.Abort()
	output := buf.String()
	if !strings.Contains(output, "\033]9;4;2;0\033\\") {
		t.Errorf("expected OSC 9;4 error sequence on Abort, got %q", output)
	}
	if !strings.Contains(output, "\033[?25h") {
		t.Errorf("expected cursor restore code on Abort, got %q", output)
	}
	if !strings.HasSuffix(output, "\n") {
		t.Errorf("expected trailing newline on Abort, got %q", output)
	}
}
