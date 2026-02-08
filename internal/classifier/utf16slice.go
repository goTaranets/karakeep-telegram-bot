package classifier

import "unicode/utf16"

// SliceByUTF16 returns substring of s by UTF-16 code unit offset/length (as used by Telegram entities).
// It is resilient to out-of-range offsets: it clamps to the valid range.
func SliceByUTF16(s string, off, length int) string {
	if off < 0 {
		off = 0
	}
	if length < 0 {
		length = 0
	}

	runes := []rune(s)

	// Build mapping: utf16 code unit index -> rune index.
	// We only need to find start/end rune indices for given code unit positions.
	startCU := off
	endCU := off + length

	curCU := 0
	startRI := len(runes)
	endRI := len(runes)

	for ri, r := range runes {
		if curCU >= startCU && startRI == len(runes) {
			startRI = ri
		}
		if curCU >= endCU {
			endRI = ri
			break
		}
		curCU += len(utf16.Encode([]rune{r}))
	}

	// If start/end are after the last rune, they should clamp to len(runes).
	if startCU >= curCU {
		startRI = len(runes)
	}
	if endCU >= curCU {
		endRI = len(runes)
	}
	if startRI > endRI {
		startRI = endRI
	}

	return string(runes[startRI:endRI])
}

