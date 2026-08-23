package main

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
)

// embedText is the server-owned embedding boundary. Deployments can replace this
// deterministic fallback with their provider adapter without changing the API.
func embedText(text string) []float64 {
	const dimension = 64
	out := make([]float64, dimension)
	for i := 0; i < len(text); i += 64 {
		sum := sha256.Sum256([]byte(text[i:min(i+64, len(text))]))
		for j := 0; j < len(sum); j += 4 {
			out[(i+j/4)%dimension] += float64(int32(binary.BigEndian.Uint32(sum[j:j+4]))) / float64(math.MaxInt32)
		}
	}
	norm := 0.0
	for _, v := range out {
		norm += v * v
	}
	norm = math.Sqrt(norm)
	if norm > 0 {
		for i := range out {
			out[i] /= norm
		}
	}
	return out
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func cosine(a, b []float64) float64 {
	n := min(len(a), len(b))
	s := 0.0
	for i := 0; i < n; i++ {
		s += a[i] * b[i]
	}
	return s
}
