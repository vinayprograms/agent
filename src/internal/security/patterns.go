package security

import (
	"regexp"
	"strings"
)

// EncodingType represents a detected encoding format.
type EncodingType string

const (
	EncodingNone      EncodingType = ""
	EncodingBase64    EncodingType = "base64"
	EncodingBase64URL EncodingType = "base64url"
	EncodingHex       EncodingType = "hex"
	EncodingURL       EncodingType = "url"
)

// EncodingDetection holds the result of encoding detection.
type EncodingDetection struct {
	Detected bool
	Type     EncodingType
	Segment  string
	Entropy  float64
}

var (
	// Base64 pattern: length multiple of 4, ends with 0-2 '=' padding
	base64Pattern = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)

	// Base64URL pattern: URL-safe variant
	base64URLPattern = regexp.MustCompile(`^[A-Za-z0-9\-_]+={0,2}$`)

	// Hex pattern: even length, only hex chars
	hexPattern = regexp.MustCompile(`^[0-9a-fA-F]+$`)

	// URL encoding: multiple %XX sequences
	urlEncodingPattern = regexp.MustCompile(`(%[0-9A-Fa-f]{2}){3,}`)
)

// DetectEncoding analyzes content for potential encoded payloads.
func DetectEncoding(content string) EncodingDetection {
	// First check for URL encoding anywhere in the string
	if urlEncodingPattern.MatchString(content) {
		return EncodingDetection{
			Detected: true,
			Type:     EncodingURL,
			Segment:  urlEncodingPattern.FindString(content),
		}
	}

	// Find long alphanumeric segments
	segments := extractAlphanumericSegments(content, 50)

	for _, seg := range segments {
		// Check entropy first - encoded content has high entropy
		entropy := ShannonEntropy([]byte(seg))
		if entropy < EntropyThreshold {
			continue
		}

		// Check for Base64
		if isValidBase64(seg) {
			return EncodingDetection{
				Detected: true,
				Type:     EncodingBase64,
				Segment:  seg,
				Entropy:  entropy,
			}
		}

		// Check for Base64URL
		if isValidBase64URL(seg) {
			return EncodingDetection{
				Detected: true,
				Type:     EncodingBase64URL,
				Segment:  seg,
				Entropy:  entropy,
			}
		}

		// Check for hex
		if isValidHex(seg) {
			return EncodingDetection{
				Detected: true,
				Type:     EncodingHex,
				Segment:  seg,
				Entropy:  entropy,
			}
		}
	}

	return EncodingDetection{Detected: false}
}

// isValidBase64 checks if a string could be valid Base64.
func isValidBase64(s string) bool {
	// Must be at least 4 chars
	if len(s) < 4 {
		return false
	}

	// Length must be multiple of 4
	if len(s)%4 != 0 {
		return false
	}

	return base64Pattern.MatchString(s)
}

// isValidBase64URL checks if a string could be valid Base64URL.
func isValidBase64URL(s string) bool {
	if len(s) < 4 {
		return false
	}

	// Base64URL can have padding or not
	return base64URLPattern.MatchString(s)
}

// isValidHex checks if a string could be valid hex encoding.
func isValidHex(s string) bool {
	// Must be even length
	if len(s)%2 != 0 {
		return false
	}

	// Must be at least 16 chars (8 bytes) to be meaningful
	if len(s) < 16 {
		return false
	}

	return hexPattern.MatchString(s)
}

// HasEncodedContent returns true if the content appears to contain encoded payloads.
func HasEncodedContent(content string) bool {
	return DetectEncoding(content).Detected
}

// SuspiciousPattern represents a pattern that may indicate prompt injection.
type SuspiciousPattern struct {
	Name    string
	Pattern *regexp.Regexp
}

var suspiciousPatterns = []SuspiciousPattern{
	// Instruction override attempts
	{Name: "ignore_previous", Pattern: regexp.MustCompile(`(?i)ignore\s+(previous|above|prior|all)\s+(instruction|directive|rule)`)},
	{Name: "new_instruction", Pattern: regexp.MustCompile(`(?i)new\s+(instruction|directive|task|policy)`)},
	{Name: "forget_previous", Pattern: regexp.MustCompile(`(?i)forget\s+(previous|everything|all)`)},

	// Superseding attempts (immutability violation)
	{Name: "update_policy", Pattern: regexp.MustCompile(`(?i)(update|change|modify)\s+(the\s+)?(policy|rule|instruction)`)},
	{Name: "override", Pattern: regexp.MustCompile(`(?i)override`)},
	{Name: "supersede", Pattern: regexp.MustCompile(`(?i)supersede`)},
	{Name: "disregard_previous", Pattern: regexp.MustCompile(`(?i)disregard\s+(previous|above|prior)`)},

	// System prompt extraction
	{Name: "reveal_prompt", Pattern: regexp.MustCompile(`(?i)(reveal|show|print|display)\s+(your\s+)?(system\s+)?prompt`)},
	{Name: "what_instructions", Pattern: regexp.MustCompile(`(?i)what\s+(are\s+)?(your\s+)?instructions`)},

	// Code execution attempts
	{Name: "execute_code", Pattern: regexp.MustCompile(`(?i)(execute|run|call|eval)\s*\(`)},
	{Name: "curl_pipe_bash", Pattern: regexp.MustCompile(`(?i)curl\s+.+\|\s*(ba)?sh`)},
	{Name: "wget_pipe_bash", Pattern: regexp.MustCompile(`(?i)wget\s+.+\|\s*(ba)?sh`)},
}

// Sensitive keywords - simple word matches (not injection patterns)
// These indicate content that may contain or reference credentials
var sensitiveKeywords = []string{
	"api_key",
	"api-key",
	"apikey",
	"password",
	"secret",
	"credential",
	"private_key",
	"access_token",
}

// KeywordMatch represents a matched sensitive keyword.
type KeywordMatch struct {
	Keyword string
}

// PatternMatch represents a matched suspicious pattern.
type PatternMatch struct {
	Name    string
	Pattern string
	Match   string
}

// DetectSuspiciousPatterns scans content for injection-related patterns.
func DetectSuspiciousPatterns(content string) []PatternMatch {
	var matches []PatternMatch

	for _, sp := range suspiciousPatterns {
		if match := sp.Pattern.FindString(content); match != "" {
			matches = append(matches, PatternMatch{
				Name:    sp.Name,
				Pattern: sp.Pattern.String(),
				Match:   match,
			})
		}
	}

	return matches
}

// DetectSensitiveKeywords scans content for sensitive keywords (not patterns).
func DetectSensitiveKeywords(content string) []KeywordMatch {
	var matches []KeywordMatch
	lowerContent := strings.ToLower(content)

	for _, kw := range sensitiveKeywords {
		if strings.Contains(lowerContent, strings.ToLower(kw)) {
			matches = append(matches, KeywordMatch{Keyword: kw})
		}
	}

	return matches
}

// HasSensitiveKeywords returns true if any sensitive keywords are detected.
func HasSensitiveKeywords(content string) bool {
	return len(DetectSensitiveKeywords(content)) > 0
}

// HasSuspiciousPatterns returns true if any suspicious patterns are detected.
func HasSuspiciousPatterns(content string) bool {
	return len(DetectSuspiciousPatterns(content)) > 0
}

// ContainsSuspiciousContent checks both patterns and encoding.
func ContainsSuspiciousContent(content string) (suspicious bool, reasons []string) {
	// Check patterns
	patterns := DetectSuspiciousPatterns(content)
	for _, p := range patterns {
		reasons = append(reasons, "pattern:"+p.Name)
	}

	// Check encoding
	enc := DetectEncoding(content)
	if enc.Detected {
		reasons = append(reasons, "encoding:"+string(enc.Type))
	}

	return len(reasons) > 0, reasons
}

// lowercaseContains checks if haystack contains needle (case-insensitive).
func lowercaseContains(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}
