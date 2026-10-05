package logs

import (
	"context"
	"github.com/szhjia/stackharbor/internal/model"
	"strings"
	"testing"
)

func TestReviewLongLineRetainsTruncationNotice(t *testing.T) {
	s := NewStore()
	Consume(context.Background(), strings.NewReader(strings.Repeat("x", 20000)+"\n"), func(line string) { s.Append(Entry{ServiceID: "a/x", Text: line}) })
	e := s.Entries([]model.ServiceID{"a/x"})
	if !strings.Contains(e[0].Text, "truncated") {
		t.Fatalf("truncation notice absent: length=%d tail=%q", len(e[0].Text), e[0].Text[len(e[0].Text)-20:])
	}
}

func TestInvalidUTF8AtEOFKeepsFollowingText(t *testing.T) {
	var got string
	Consume(context.Background(), strings.NewReader("begin\xfftail"), func(line string) { got = line })
	if !strings.Contains(got, "tail") {
		t.Fatal("invalid byte erased following valid text", got)
	}
}
