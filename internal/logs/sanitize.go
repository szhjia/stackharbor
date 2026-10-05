package logs

import (
	"strings"
	"unicode/utf8"
)

func Sanitize(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 27 {
			i++
			if i >= len(s) {
				break
			}
			kind := s[i]
			i++
			switch kind {
			case '[':
				for i < len(s) {
					b := s[i]
					i++
					if b >= 0x40 && b <= 0x7e {
						break
					}
				}
			case ']', 'P', '^', '_':
				for i < len(s) {
					if s[i] == 7 {
						i++
						break
					}
					if s[i] == 27 && i+1 < len(s) && s[i+1] == '\\' {
						i += 2
						break
					}
					i++
				}
			}
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		if r == '\t' {
			out.WriteString("    ")
		} else if r >= 32 && r != 127 && !(r >= 128 && r <= 159) {
			out.WriteRune(r)
		}
	}
	return out.String()
}
