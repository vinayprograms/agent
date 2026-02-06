package security

import (
	"math"
)

// ShannonEntropy calculates the Shannon entropy of a byte slice in bits per byte.
// Higher entropy indicates more randomness/compression/encoding.
//
// Typical values:
//   - English text: 3.0 - 4.5 bits/byte
//   - Source code: 4.0 - 5.0 bits/byte
//   - Base64 encoded: 5.5 - 6.0 bits/byte
//   - Compressed/random: 7.5 - 8.0 bits/byte
func ShannonEntropy(data []byte) float64 {
	if len(data) == 0 {
		return 0
	}

	// Count byte frequencies
	freq := make(map[byte]int)
	for _, b := range data {
		freq[b]++
	}

	// Calculate entropy
	length := float64(len(data))
	var entropy float64

	for _, count := range freq {
		if count == 0 {
			continue
		}
		p := float64(count) / length
		entropy -= p * math.Log2(p)
	}

	return entropy
}

// EntropyThreshold is the bits/byte threshold above which content is considered
// potentially encoded. Base64 typically has entropy around 5.0-5.5, so we use a
// lower threshold to catch it.
const EntropyThreshold = 4.8

// IsHighEntropy returns true if the content has entropy above the threshold.
func IsHighEntropy(data []byte) bool {
	return ShannonEntropy(data) > EntropyThreshold
}

// SegmentEntropy calculates entropy for segments of a string.
// Returns the segment with highest entropy and its value.
// This is useful for detecting encoded payloads embedded in normal text.
func SegmentEntropy(content string, minSegmentLen int) (highestSegment string, highestEntropy float64) {
	if minSegmentLen < 10 {
		minSegmentLen = 50 // Default minimum for meaningful entropy calculation
	}

	// Find alphanumeric segments (potential encoded content)
	segments := extractAlphanumericSegments(content, minSegmentLen)

	for _, seg := range segments {
		entropy := ShannonEntropy([]byte(seg))
		if entropy > highestEntropy {
			highestEntropy = entropy
			highestSegment = seg
		}
	}

	return highestSegment, highestEntropy
}

// extractAlphanumericSegments finds contiguous alphanumeric strings of at least minLen.
func extractAlphanumericSegments(content string, minLen int) []string {
	var segments []string
	var current []byte

	for i := 0; i < len(content); i++ {
		c := content[i]
		if isBase64Char(c) {
			current = append(current, c)
		} else {
			if len(current) >= minLen {
				segments = append(segments, string(current))
			}
			current = current[:0]
		}
	}

	// Don't forget the last segment
	if len(current) >= minLen {
		segments = append(segments, string(current))
	}

	return segments
}

// isBase64Char returns true if the byte could be part of Base64 encoding.
func isBase64Char(c byte) bool {
	return (c >= 'A' && c <= 'Z') ||
		(c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9') ||
		c == '+' || c == '/' || c == '=' ||
		c == '-' || c == '_' // Base64URL variants
}
