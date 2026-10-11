//go:build amd64

package analyze

func hasAVX2() bool
func hasFMA() bool
func sumSquaresAVX2(samples []float32) float64
func peakAbsAVX2(samples []float32) float32

var (
	cpuHasAVX2 = hasAVX2()
	cpuHasFMA  = hasFMA()
)

func sumSquares(samples []float32) float64 {
	if cpuHasAVX2 && cpuHasFMA && len(samples) >= 8 {
		return sumSquaresAVX2(samples)
	}
	return sumSquaresGeneric(samples)
}

func peakAbs(samples []float32) float32 {
	if cpuHasAVX2 && len(samples) >= 8 {
		return peakAbsAVX2(samples)
	}
	return peakAbsGeneric(samples)
}
