package analyze

import (
	"fmt"
	"math"

	"codeberg.org/nathawat/lecturejet/internal/config"
	"codeberg.org/nathawat/lecturejet/internal/timeline"
)

type WindowMetric struct {
	StartSample int64
	Samples     int
	RMS         float32
	Peak        float32
	ZCR         int
}

type Detector struct {
	sampleRate   int
	minSamples   int64
	marginNS     timeline.TimeNS
	silenceLevel float32
	state        detectorState
	quietStart   int64
	lastSample   int64
	cuts         []timeline.Segment
}

type detectorState uint8

const (
	stateSpeech detectorState = iota
	stateMaybeSilence
)

func NewDetector(cfg config.SilenceConfig, sampleRate int) (*Detector, error) {
	if sampleRate <= 0 {
		return nil, fmt.Errorf("sample rate must be positive")
	}
	if cfg.WindowMS <= 0 || cfg.MinSilenceMS < cfg.WindowMS || cfg.MarginMS < 0 ||
		math.IsNaN(cfg.ThresholdDB) || math.IsInf(cfg.ThresholdDB, 0) {
		return nil, fmt.Errorf("invalid silence configuration")
	}
	threshold := float32(math.Pow(10, cfg.ThresholdDB/20))
	return &Detector{
		sampleRate:   sampleRate,
		minSamples:   int64(cfg.MinSilenceMS) * int64(sampleRate) / 1000,
		marginNS:     timeline.TimeNS(cfg.MarginMS) * 1_000_000,
		silenceLevel: threshold,
	}, nil
}

func (d *Detector) Consume(metrics []WindowMetric) {
	for _, metric := range metrics {
		if metric.Samples <= 0 {
			continue
		}
		end := metric.StartSample + int64(metric.Samples)
		if end > d.lastSample {
			d.lastSample = end
		}
		level := metric.RMS
		if metric.Peak > level {
			level = metric.Peak
		}
		silent := level < d.silenceLevel
		switch d.state {
		case stateSpeech:
			if silent {
				d.state = stateMaybeSilence
				d.quietStart = metric.StartSample
			}
		case stateMaybeSilence:
			if !silent {
				d.finishCandidate(metric.StartSample)
				d.state = stateSpeech
			}
		}
	}
}

func (d *Detector) Finish(sourceSamples int64) []timeline.Segment {
	if sourceSamples > d.lastSample {
		d.lastSample = sourceSamples
	}
	if d.state == stateMaybeSilence {
		d.finishCandidate(d.lastSample)
		d.state = stateSpeech
	}
	return append([]timeline.Segment(nil), d.cuts...)
}

func (d *Detector) finishCandidate(endSample int64) {
	if endSample <= d.quietStart || endSample-d.quietStart < d.minSamples {
		return
	}
	start := timeline.TimeForSamples(d.quietStart, d.sampleRate) + d.marginNS
	end := timeline.TimeForSamples(endSample, d.sampleRate) - d.marginNS
	if end <= start {
		return
	}
	d.cuts = append(d.cuts, timeline.Segment{Start: start, End: end, Action: timeline.Cut})
}

func RMS(samples []float32) float32 {
	if len(samples) == 0 {
		return 0
	}
	sum := sumSquares(samples)
	return float32(math.Sqrt(sum / float64(len(samples))))
}

func ZeroCrossingRate(samples []float32) int {
	return zeroCrossingRate(samples)
}

func AnalyzeWindow(samples []float32) WindowMetric {
	n := len(samples)
	if n == 0 {
		return WindowMetric{}
	}
	sum := sumSquares(samples)
	peak := peakAbs(samples)
	if !IsFinite(float32(sum)) || !IsFinite(peak) {
		// Never turn corrupt PCM into an apparent silent window.
		return WindowMetric{Samples: n, RMS: 1, Peak: 1}
	}
	rms := float32(math.Sqrt(sum / float64(n)))
	zcr := zeroCrossingRate(samples)
	return WindowMetric{Samples: n, RMS: rms, Peak: peak, ZCR: zcr}
}

func IsFinite(value float32) bool {
	return math.Float32bits(value)&0x7f800000 != 0x7f800000
}

func Windows(samples []float32, sampleRate, windowMS int) []WindowMetric {
	if sampleRate <= 0 || windowMS <= 0 {
		return nil
	}
	windowSamples := sampleRate * windowMS / 1000
	if windowSamples < 1 {
		windowSamples = 1
	}
	metrics := make([]WindowMetric, 0, (len(samples)+windowSamples-1)/windowSamples)
	for start := 0; start < len(samples); start += windowSamples {
		end := start + windowSamples
		if end > len(samples) {
			end = len(samples)
		}
		metric := AnalyzeWindow(samples[start:end])
		metric.StartSample = int64(start)
		metrics = append(metrics, metric)
	}
	return metrics
}
