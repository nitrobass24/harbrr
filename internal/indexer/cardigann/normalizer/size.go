package normalizer

import (
	"math"
	"strconv"
	"strings"
)

// sizeStep is the single 1024 multiplier Jackett applies per magnitude in its
// BytesFromKB/MB/GB/TB chain. The whole chain runs in float32 (each helper
// takes a float and multiplies by 1024f), so harbrr must compute in float32
// too — a float64 computation diverges from Jackett's truncated int64 by tens
// of bytes at GB/TB magnitudes, which would silently bake a non-Jackett value
// into the parity-comparison unit.
const sizeStep float32 = 1024

// parseSize reproduces Jackett's ParseUtil.GetBytes byte-for-byte:
//
//   - The numeric part keeps only digits, '.' and ','; ',' becomes '.'; when
//     more than one '.' survives, all but the last are thousands separators and
//     dropped (so "1.018,29 MB" -> 1018.29 MB).
//   - The unit is the letters only, with 'i' stripped and lowercased, so KiB and
//     KB are identical. The unit match is by Contains in KB→MB→GB→TB order.
//   - The byte count is (long)(value * multiplier) computed in float32 and
//     truncated toward zero, matching Jackett's float32 BytesFrom* chain.
//   - A value with no recognised unit is treated as a raw byte count.
//   - Empty/"-"/"---" coerce to 0.
func parseSize(s string) int64 {
	val := coerceFloatForSize(numericPart(s))
	unit := strings.ToLower(strings.ReplaceAll(lettersOnly(s), "i", ""))

	switch {
	case strings.Contains(unit, "kb"):
		return bytesFromKB(val)
	case strings.Contains(unit, "mb"):
		return bytesFromKB(val * sizeStep)
	case strings.Contains(unit, "gb"):
		return bytesFromKB(val * sizeStep * sizeStep)
	case strings.Contains(unit, "tb"):
		return bytesFromKB(val * sizeStep * sizeStep * sizeStep)
	default:
		return truncBytes(val)
	}
}

// bytesFromKB mirrors ParseUtil.BytesFromKB: (long)(kb * 1024f), with the
// multiply and the truncating cast both in float32.
func bytesFromKB(kb float32) int64 {
	return truncBytes(kb * sizeStep)
}

// numericPart extracts the value substring exactly as GetBytes does before
// CoerceFloat: keep digits/'.'/',', ',' -> '.', collapse extra '.' to grouping.
func numericPart(s string) string {
	kept := keepNumeric(s)
	if kept == "" {
		return "0"
	}
	return normalizeDecimal(kept)
}

// coerceFloatForSize parses the already-normalized size value as float32,
// mirroring Jackett's CoerceFloat (float.Parse); unparseable input yields 0,
// matching CoerceFloat's lenient "0" fallback.
func coerceFloatForSize(s string) float32 {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 32)
	if err != nil {
		return 0
	}
	return float32(f)
}

// lettersOnly returns the Unicode letters of s, mirroring GetBytes's
// str.Where(char.IsLetter) unit extraction.
func lettersOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// truncBytes converts a float32 byte count to int64 truncating toward zero,
// matching the C# (long) cast in BytesFromKB. Non-finite or overflowing values
// clamp rather than panic.
func truncBytes(v float32) int64 {
	t := math.Trunc(float64(v))
	if math.IsNaN(t) || t <= math.MinInt64 {
		return 0
	}
	if t >= math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(t)
}
