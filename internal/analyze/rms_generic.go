package analyze

func sumSquaresGeneric(samples []float32) float64 {
	n := len(samples)
	if n == 0 {
		return 0
	}
	_ = samples[n-1]

	var s0, s1, s2, s3 float64
	var s4, s5, s6, s7 float64

	i := 0
	for ; i+7 < n; i += 8 {
		x0, x1, x2, x3 := float64(samples[i]), float64(samples[i+1]), float64(samples[i+2]), float64(samples[i+3])
		x4, x5, x6, x7 := float64(samples[i+4]), float64(samples[i+5]), float64(samples[i+6]), float64(samples[i+7])

		s0 += x0 * x0
		s1 += x1 * x1
		s2 += x2 * x2
		s3 += x3 * x3
		s4 += x4 * x4
		s5 += x5 * x5
		s6 += x6 * x6
		s7 += x7 * x7
	}

	sum := (s0 + s1) + (s2 + s3) + (s4 + s5) + (s6 + s7)
	for ; i < n; i++ {
		x := float64(samples[i])
		sum += x * x
	}
	return sum
}
