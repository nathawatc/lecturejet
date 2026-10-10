package analyze

import "math"

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

func zcrGeneric(samples []float32) int {
	n := len(samples)
	if n < 2 {
		return 0
	}
	_ = samples[n-1]

	count := 0
	i := 0
	for ; i+8 < n; i += 8 {
		b0 := math.Float32bits(samples[i])
		b1 := math.Float32bits(samples[i+1])
		b2 := math.Float32bits(samples[i+2])
		b3 := math.Float32bits(samples[i+3])
		b4 := math.Float32bits(samples[i+4])
		b5 := math.Float32bits(samples[i+5])
		b6 := math.Float32bits(samples[i+6])
		b7 := math.Float32bits(samples[i+7])
		b8 := math.Float32bits(samples[i+8])

		c0 := (b0 ^ b1) >> 31
		c1 := (b1 ^ b2) >> 31
		c2 := (b2 ^ b3) >> 31
		c3 := (b3 ^ b4) >> 31
		c4 := (b4 ^ b5) >> 31
		c5 := (b5 ^ b6) >> 31
		c6 := (b6 ^ b7) >> 31
		c7 := (b7 ^ b8) >> 31

		count += int(c0 + c1 + c2 + c3 + c4 + c5 + c6 + c7)
	}

	for ; i < n-1; i++ {
		b0 := math.Float32bits(samples[i])
		b1 := math.Float32bits(samples[i+1])
		count += int((b0 ^ b1) >> 31)
	}
	return count
}

func peakAbs(samples []float32) float32 {
	n := len(samples)
	if n == 0 {
		return 0
	}
	_ = samples[n-1]

	var p0, p1, p2, p3 float32
	var p4, p5, p6, p7 float32

	i := 0
	for ; i+7 < n; i += 8 {
		s0, s1, s2, s3 := samples[i], samples[i+1], samples[i+2], samples[i+3]
		s4, s5, s6, s7 := samples[i+4], samples[i+5], samples[i+6], samples[i+7]

		if s0 < 0 {
			s0 = -s0
		}
		if s1 < 0 {
			s1 = -s1
		}
		if s2 < 0 {
			s2 = -s2
		}
		if s3 < 0 {
			s3 = -s3
		}
		if s4 < 0 {
			s4 = -s4
		}
		if s5 < 0 {
			s5 = -s5
		}
		if s6 < 0 {
			s6 = -s6
		}
		if s7 < 0 {
			s7 = -s7
		}

		if s0 > p0 {
			p0 = s0
		}
		if s1 > p1 {
			p1 = s1
		}
		if s2 > p2 {
			p2 = s2
		}
		if s3 > p3 {
			p3 = s3
		}
		if s4 > p4 {
			p4 = s4
		}
		if s5 > p5 {
			p5 = s5
		}
		if s6 > p6 {
			p6 = s6
		}
		if s7 > p7 {
			p7 = s7
		}
	}

	peak := p0
	if p1 > peak {
		peak = p1
	}
	if p2 > peak {
		peak = p2
	}
	if p3 > peak {
		peak = p3
	}
	if p4 > peak {
		peak = p4
	}
	if p5 > peak {
		peak = p5
	}
	if p6 > peak {
		peak = p6
	}
	if p7 > peak {
		peak = p7
	}

	for ; i < n; i++ {
		s := samples[i]
		if s < 0 {
			s = -s
		}
		if s > peak {
			peak = s
		}
	}

	return peak
}
