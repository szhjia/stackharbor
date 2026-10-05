package logs

import (
	"context"
	"io"
	"strings"
	"unicode/utf8"
)

func Consume(ctx context.Context, r io.Reader, emit func(string)) error {
	buf := make([]byte, 4096)
	line := make([]byte, 0, 16384)
	truncated := false
	flush := func() {
		s := string(line)
		s = strings.ToValidUTF8(s, "�")
		s = strings.TrimSuffix(s, "\r")
		if truncated {
			s += " …[truncated]"
		}
		emit(truncateLine(s))
		line = line[:0]
		truncated = false
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := r.Read(buf)
		for _, b := range buf[:n] {
			if b == '\n' {
				flush()
			} else if len(line) < 16384 {
				line = append(line, b)
			} else {
				truncated = true
			}
		}
		if err != nil {
			if len(line) > 0 || truncated {
				flush()
			}
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func truncateLine(s string) string {
	const limit = 16384
	const suffix = " …[truncated]"
	s = strings.ToValidUTF8(s, "�")
	if len(s) <= limit {
		return s
	}
	s = s[:limit-len(suffix)]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + suffix
}
