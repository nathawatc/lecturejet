package app

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestProgressReporterTTY(t *testing.T) {
	var buf bytes.Buffer
	reporter := newProgressReporterWithTTY(&buf, 2, 3, "Rendering HEVC VAAPI...", true)

	reporter.Update(78.4, 25*time.Second)
	output := buf.String()

	if !strings.Contains(output, "\033]9;4;1;") {
		t.Errorf("expected OSC 9;4 in-progress escape sequence, got %q", output)
	}
	if !strings.Contains(output, "\r\033[2K") {
		t.Errorf("expected clear-line escape code, got %q", output)
	}
	if !strings.Contains(output, "[Phase 2/3] Rendering HEVC VAAPI...") {
		t.Errorf("expected phase 2/3 indicator, got %q", output)
	}
	if !strings.Contains(output, "78.4%") {
		t.Errorf("expected percentage 78.4%%, got %q", output)
	}
	if !strings.Contains(output, "█") {
		t.Errorf("expected progress bar blocks, got %q", output)
	}

	buf.Reset()
	reporter.Finish()
	finishOutput := buf.String()
	if !strings.Contains(finishOutput, "\033]9;4;0;0\033\\") {
		t.Errorf("expected OSC 9;4 completed sequence on Finish, got %q", finishOutput)
	}
	if !strings.HasSuffix(finishOutput, "\n") {
		t.Errorf("expected trailing newline on Finish, got %q", finishOutput)
	}
}

func TestProgressReporterNonTTY(t *testing.T) {
	var buf bytes.Buffer
	reporter := newProgressReporterWithTTY(&buf, 1, 3, "Analyzing audio silence...", false)

	reporter.Update(50.0, 10*time.Second)
	output := buf.String()

	if strings.Contains(output, "\033]") {
		t.Errorf("expected no OSC escape sequences in non-TTY mode, got %q", output)
	}
	if strings.Contains(output, "\033[2K") {
		t.Errorf("expected no clear-line escape code in non-TTY mode, got %q", output)
	}
	if !strings.Contains(output, "[Phase 1/3] Analyzing audio silence...") {
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
	reporter := newProgressReporterWithTTY(&buf, 2, 3, "Rendering...", false)

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
	reporter := newProgressReporterWithTTY(&buf, 1, 1, "Testing...", false)

	reporter.Update(10.0, 0)
	buf.Reset()

	// Update with only +0.1% immediately afterwards should be throttled
	reporter.Update(10.1, 0)
	if buf.Len() > 0 {
		t.Errorf("expected update to be throttled, got %q", buf.String())
	}

	// Update with >= +0.5% should bypass time throttling
	reporter.Update(10.6, 0)
	if buf.Len() == 0 {
		t.Errorf("expected update >= 0.5%% to be rendered, got nothing")
	}
}

func TestProgressReporterAbortTTY(t *testing.T) {
	var buf bytes.Buffer
	reporter := newProgressReporterWithTTY(&buf, 2, 3, "Rendering...", true)

	reporter.Abort()
	output := buf.String()
	if !strings.Contains(output, "\033]9;4;2;0\033\\") {
		t.Errorf("expected OSC 9;4 error sequence on Abort, got %q", output)
	}
	if !strings.HasSuffix(output, "\n") {
		t.Errorf("expected trailing newline on Abort, got %q", output)
	}
}
