//go:build !amd64

package analyze

func sumSquares(samples []float32) float64 {
	return sumSquaresGeneric(samples)
}
