//go:build amd64

package analyze

func hasAVX2() bool
func sumSquaresAVX2(samples []float32) float64
func zcrAVX2(samples []float32) int

var cpuHasAVX2 = hasAVX2()

func sumSquares(samples []float32) float64 {
	if cpuHasAVX2 && len(samples) >= 8 {
		return sumSquaresAVX2(samples)
	}
	return sumSquaresGeneric(samples)
}

func zeroCrossingRate(samples []float32) int {
	if cpuHasAVX2 && len(samples) >= 8 {
		return zcrAVX2(samples)
	}
	return zcrGeneric(samples)
}
