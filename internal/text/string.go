package text

// decodeString turns a matched `'"' character* '"'` span into its bytes, per the
// reference's `string` function (lexer.mll:24–49).
//
// The escapes produce *bytes*, not runes: `\ff` is one byte 0xFF and not U+00FF. That
// distinction is load-bearing for the 186 `malformed UTF-8 encoding` vectors — they are
// invalid *because* the escape produces a raw byte the name decoder then rejects, so a
// decoder that helpfully re-encoded it as UTF-8 would make every one of them well-formed
// and the bucket would go green while being wrong. The sibling wast reader learned the
// same thing (`"\ff"` → one raw byte) and it is asserted there too.
//
// `\u{...}` is the one escape that *does* produce UTF-8: the reference encodes the
// scalar value (lexer.mll via Utf8.encode). So the two cases are deliberately different,
// and a test pins both.
func decodeString(s []byte) ([]byte, bool) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return nil, false
	}
	body := s[1 : len(s)-1]
	// Non-nil for the empty string: emptiness is a length, never a nil. A caller testing
	// `Value != nil` to mean "has a string" would otherwise misread `""`, which is a real
	// vector and was a fuzz finding in the wast reader.
	out := make([]byte, 0, len(body))
	for i := 0; i < len(body); {
		c := body[i]
		if c != '\\' {
			out = append(out, c)
			i++
			continue
		}
		if i+1 >= len(body) {
			return nil, false
		}
		switch body[i+1] {
		case 'n':
			out = append(out, '\n')
			i += 2
		case 'r':
			out = append(out, '\r')
			i += 2
		case 't':
			out = append(out, '\t')
			i += 2
		case '\\':
			out = append(out, '\\')
			i += 2
		case '\'':
			out = append(out, '\'')
			i += 2
		case '"':
			out = append(out, '"')
			i += 2
		case 'u':
			n, v, ok := decodeUnicodeEscape(body[i:])
			if !ok {
				return nil, false
			}
			out = appendUTF8(out, v)
			i += n
		default:
			if i+2 < len(body) && isHexDigit(body[i+1]) && isHexDigit(body[i+2]) {
				out = append(out, hexVal(body[i+1])<<4|hexVal(body[i+2]))
				i += 3
				continue
			}
			return nil, false
		}
	}
	return out, true
}

// decodeUnicodeEscape reads `\u{hexnum}` and returns its length, the scalar value, and
// whether it parsed. Underscore separators are legal inside hexnum.
func decodeUnicodeEscape(b []byte) (length int, value uint32, ok bool) {
	if !hasPrefix(b, "\\u{") {
		return 0, 0, false
	}
	n := matchHexNum(b[3:])
	if n <= 0 || 3+n >= len(b) || b[3+n] != '}' {
		return 0, 0, false
	}
	var v uint64
	for _, c := range b[3 : 3+n] {
		if c == '_' {
			continue
		}
		v = v<<4 | uint64(hexVal(c))
		if v > 0x10FFFF {
			return 0, 0, false
		}
	}
	return 4 + n, uint32(v), true
}

func hexVal(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

// appendUTF8 encodes a scalar value, including the surrogate range — which utf8.AppendRune
// would silently replace with U+FFFD. The reference's Utf8.encode does not substitute, and
// a substitution here would turn a vector the spec calls malformed into a well-formed one.
// That is why this is hand-rolled rather than delegated.
func appendUTF8(dst []byte, v uint32) []byte {
	switch {
	case v < 0x80:
		return append(dst, byte(v))
	case v < 0x800:
		return append(dst, byte(0xC0|v>>6), byte(0x80|v&0x3F))
	case v < 0x10000:
		return append(dst, byte(0xE0|v>>12), byte(0x80|v>>6&0x3F), byte(0x80|v&0x3F))
	default:
		return append(dst, byte(0xF0|v>>18), byte(0x80|v>>12&0x3F),
			byte(0x80|v>>6&0x3F), byte(0x80|v&0x3F))
	}
}
