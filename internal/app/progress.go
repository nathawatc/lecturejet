package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"codeberg.org/nathawat/lecturejet/internal/timeline"
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
	mu             sync.Mutex
	w              io.Writer
	isTTY          bool
	phase          int
	totalPhases    int
	stageName      string
	headerPrinted  bool
	drawnLines     int
	startTime      time.Time
	lastUpdate     time.Time
	lastPct        float64
	lastOSCPct     int
	lastMediaDur   time.Duration
	smoothedSpeed  float64
	lastNonTTYPct  float64
	lastNonTTYTime time.Time

	isRender      bool
	totalCuts     int
	cutEnds       []timeline.TimeNS
	totalDuration time.Duration

	finished bool
	aborted  bool
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
		w:             w,
		isTTY:         isTTY,
		phase:         phase,
		totalPhases:   totalPhases,
		stageName:     stageName,
		startTime:     time.Now(),
		lastPct:       -1,
		lastOSCPct:    -1,
		lastNonTTYPct: -10,
	}
}

func (p *ProgressReporter) SetRenderMetrics(totalCuts int, cutEnds []timeline.TimeNS, totalDuration time.Duration) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.isRender = true
	p.totalCuts = totalCuts
	p.cutEnds = cutEnds
	p.totalDuration = totalDuration
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
	p.lastMediaDur = 0
	p.smoothedSpeed = 0
	p.headerPrinted = false
	p.drawnLines = 0
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
	if pct < 100 && p.lastPct >= 0 && pct-p.lastPct < 0.2 && now.Sub(p.lastUpdate) < 50*time.Millisecond {
		return
	}

	p.render(pct, mediaDuration)
}

func formatDurationHMS(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	totalSeconds := int64(d.Round(time.Second).Seconds())
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
}

func (p *ProgressReporter) render(pct float64, mediaDuration time.Duration) {
	now := time.Now()
	if !p.headerPrinted {
		if p.isTTY {
			fmt.Fprintf(p.w, "\033[?25l[%d/%d] %s\n", p.phase, p.totalPhases, p.stageName)
		} else {
			fmt.Fprintf(p.w, "[%d/%d] %s\n", p.phase, p.totalPhases, p.stageName)
		}
		p.headerPrinted = true
	}

	elapsed := now.Sub(p.startTime)
	if mediaDuration > p.lastMediaDur && p.lastUpdate.After(p.startTime) {
		dt := now.Sub(p.lastUpdate).Seconds()
		dMedia := (mediaDuration - p.lastMediaDur).Seconds()
		if dt >= 0.05 && dMedia > 0 {
			instantSpeed := dMedia / dt
			if p.smoothedSpeed <= 0 {
				p.smoothedSpeed = instantSpeed
			} else {
				p.smoothedSpeed = 0.15*instantSpeed + 0.85*p.smoothedSpeed
			}
		}
	} else if p.smoothedSpeed <= 0 && elapsed > 100*time.Millisecond && mediaDuration > 0 {
		p.smoothedSpeed = mediaDuration.Seconds() / elapsed.Seconds()
	}
	p.lastMediaDur = mediaDuration
	p.lastPct = pct
	p.lastUpdate = now

	speed := p.smoothedSpeed
	if speed <= 0 && elapsed > 100*time.Millisecond && mediaDuration > 0 {
		speed = mediaDuration.Seconds() / elapsed.Seconds()
	}

	var overall float64
	if p.totalPhases <= 1 {
		overall = pct
	} else {
		switch p.phase {
		case 1:
			overall = pct * 0.20
		case 2:
			overall = 20.0 + pct*0.80
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

	const barWidth = 40
	var bar string
	if pct >= 100 {
		bar = strings.Repeat("━", barWidth)
	} else {
		filled := int((pct / 100.0) * float64(barWidth))
		if filled >= barWidth {
			filled = barWidth - 1
		}
		if filled < 0 {
			filled = 0
		}
		empty := barWidth - filled - 1
		if empty < 0 {
			empty = 0
		}
		if p.isTTY {
			bar = strings.Repeat("━", filled) + "╸" + "\033[90m" + strings.Repeat("━", empty) + "\033[0m"
		} else {
			bar = strings.Repeat("━", filled) + "╸" + strings.Repeat("─", empty)
		}
	}

	elapsedStr := formatDurationHMS(elapsed)

	if !p.isRender {
		var line string
		if pct >= 100 {
			line = fmt.Sprintf(" %s 100.0%% • %.1fx speed (%s)", bar, speed, elapsedStr)
		} else {
			line = fmt.Sprintf(" %s %5.1f%% • %.1fx speed (%s)", bar, pct, speed, elapsedStr)
		}

		if p.isTTY {
			fmt.Fprintf(p.w, "\r\033[2K%s", line)
			p.drawnLines = 1
		} else {
			if pct == 0 || pct >= 100 || pct-p.lastNonTTYPct >= 5.0 || now.Sub(p.lastNonTTYTime) >= 10*time.Second {
				fmt.Fprintf(p.w, "%s\n", line)
				p.lastNonTTYPct = pct
				p.lastNonTTYTime = now
			}
		}
		return
	}

	var etaStr string
	if pct >= 100 {
		etaStr = "00:00:00"
	} else if speed > 0.05 && p.totalDuration > mediaDuration {
		remSec := (p.totalDuration - mediaDuration).Seconds() / speed
		etaStr = formatDurationHMS(time.Duration(remSec * float64(time.Second)))
	} else {
		etaStr = "--:--:--"
	}

	var cutsStr string
	if p.totalCuts > 0 {
		targetNS := timeline.TimeNS(mediaDuration)
		passed := sort.Search(len(p.cutEnds), func(i int) bool {
			return p.cutEnds[i] > targetNS
		})
		if pct >= 100 {
			passed = p.totalCuts
		}
		cutsStr = fmt.Sprintf("%d/%d cuts passed", passed, p.totalCuts)
	} else {
		cutsStr = "0 cuts passed"
	}

	var line1 string
	if pct >= 100 {
		line1 = fmt.Sprintf(" %s 100.0%% • %.1fx speed", bar, speed)
	} else {
		line1 = fmt.Sprintf(" %s  %4.1f%% • %.1fx speed", bar, pct, speed)
	}
	line2 := fmt.Sprintf(" Elapsed: %s • ETA: %s • %s", elapsedStr, etaStr, cutsStr)

	if p.isTTY {
		if p.drawnLines == 2 {
			fmt.Fprintf(p.w, "\033[1A\r\033[2K%s\n\r\033[2K%s", line1, line2)
		} else {
			fmt.Fprintf(p.w, "\r\033[2K%s\n\r\033[2K%s", line1, line2)
			p.drawnLines = 2
		}
	} else {
		if pct == 0 || pct >= 100 || pct-p.lastNonTTYPct >= 5.0 || now.Sub(p.lastNonTTYTime) >= 10*time.Second {
			fmt.Fprintf(p.w, "%s\n%s\n", line1, line2)
			p.lastNonTTYPct = pct
			p.lastNonTTYTime = now
		}
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
	p.render(100, p.totalDuration)
	if p.isTTY {
		fmt.Fprintf(p.w, "\033]9;4;0;0\033\\\033[?25h\n")
	}
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
		fmt.Fprintf(p.w, "\033]9;4;2;0\033\\\033[?25h\n")
	} else {
		fmt.Fprintln(p.w)
	}
}
