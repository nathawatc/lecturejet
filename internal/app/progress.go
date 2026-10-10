package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

type workflowKey struct{}

type workflowConfig struct {
	currentPhase int
	totalPhases  int
}

func WithWorkflow(ctx context.Context, current, total int) context.Context {
	return context.WithValue(ctx, workflowKey{}, workflowConfig{current, total})
}

func getWorkflow(ctx context.Context, defaultCurrent, defaultTotal int) (int, int) {
	if v, ok := ctx.Value(workflowKey{}).(workflowConfig); ok && v.totalPhases > 0 {
		return v.currentPhase, v.totalPhases
	}
	return defaultCurrent, defaultTotal
}

func isTerminalWriter(w io.Writer) bool {
	if f, ok := w.(*os.File); ok && f != nil {
		info, err := f.Stat()
		return err == nil && info.Mode()&os.ModeCharDevice != 0
	}
	return false
}

type ProgressReporter struct {
	mu          sync.Mutex
	w           io.Writer
	isTTY       bool
	phase       int
	totalPhases int
	stageName   string
	startTime   time.Time
	lastUpdate  time.Time
	lastPct     float64
	lastOSCPct  int
	finished    bool
	aborted     bool
}

func NewProgressReporter(w io.Writer, phase, totalPhases int, stageName string) *ProgressReporter {
	if w == nil {
		return nil
	}
	return newProgressReporterWithTTY(w, phase, totalPhases, stageName, isTerminalWriter(w))
}

func newProgressReporterWithTTY(w io.Writer, phase, totalPhases int, stageName string, isTTY bool) *ProgressReporter {
	if w == nil {
		return nil
	}
	if phase <= 0 {
		phase = 1
	}
	if totalPhases <= 0 {
		totalPhases = 1
	}
	return &ProgressReporter{
		w:           w,
		isTTY:       isTTY,
		phase:       phase,
		totalPhases: totalPhases,
		stageName:   stageName,
		startTime:   time.Now(),
		lastPct:     -1,
		lastOSCPct:  -1,
	}
}

func (p *ProgressReporter) SetStage(phase int, stageName string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.phase = phase
	p.stageName = stageName
	p.startTime = time.Now()
	p.lastPct = 0
	p.render(0, 0)
}

func (p *ProgressReporter) Update(pct float64, mediaDuration time.Duration) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished || p.aborted {
		return
	}

	if pct < p.lastPct {
		pct = p.lastPct
	}
	if pct > 100 {
		pct = 100
	}

	now := time.Now()
	if pct < 100 && p.lastPct >= 0 && pct-p.lastPct < 0.5 && now.Sub(p.lastUpdate) < 50*time.Millisecond {
		return
	}

	p.render(pct, mediaDuration)
}

func (p *ProgressReporter) render(pct float64, mediaDuration time.Duration) {
	p.lastPct = pct
	p.lastUpdate = time.Now()

	var overall float64
	if p.totalPhases <= 1 {
		overall = pct
	} else {
		switch p.phase {
		case 1:
			overall = pct * 0.20
		case 2:
			overall = 20.0 + pct*0.75
		case 3:
			overall = 95.0 + pct*0.05
		default:
			overall = (float64(p.phase-1) + pct/100.0) / float64(p.totalPhases) * 100.0
		}
	}
	if overall > 100 {
		overall = 100
	}

	oscInt := int(overall)
	if p.isTTY && oscInt != p.lastOSCPct {
		fmt.Fprintf(p.w, "\033]9;4;1;%d\033\\", oscInt)
		p.lastOSCPct = oscInt
	}

	speedStr := ""
	elapsed := time.Since(p.startTime)
	if elapsed > 100*time.Millisecond && mediaDuration > 0 {
		speed := float64(mediaDuration) / float64(elapsed)
		if speed > 0.05 {
			speedStr = fmt.Sprintf(" (%.1fx speed)", speed)
		}
	}

	const barWidth = 10
	filled := int((pct / 100.0) * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	if filled < 0 {
		filled = 0
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

	line := fmt.Sprintf("[Phase %d/%d] %s [%s] %5.1f%%%s",
		p.phase, p.totalPhases, p.stageName, bar, pct, speedStr)

	if p.isTTY {
		fmt.Fprintf(p.w, "\r\033[2K%s", line)
	} else {
		fmt.Fprintf(p.w, "\r%s", line)
	}
}

func (p *ProgressReporter) Finish() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished || p.aborted {
		return
	}
	p.finished = true
	p.render(100, 0)
	if p.isTTY {
		fmt.Fprintf(p.w, "\033]9;4;0;0\033\\")
	}
	fmt.Fprintln(p.w)
}

func (p *ProgressReporter) Abort() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished || p.aborted {
		return
	}
	p.aborted = true
	if p.isTTY {
		fmt.Fprintf(p.w, "\033]9;4;2;0\033\\")
	}
	fmt.Fprintln(p.w)
}
