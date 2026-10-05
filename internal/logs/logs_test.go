package logs

import (
	"context"
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"strings"
	"testing"
)

func TestStoreHonorsLineByteAndGlobalLimits(t *testing.T) {
	s := NewStore()
	for i := 0; i < 2001; i++ {
		s.Append(Entry{ServiceID: "app/web", Text: "line"})
	}
	if len(s.Entries([]model.ServiceID{"app/web"})) != 2000 || s.Dropped("app/web") != 1 {
		t.Fatal("line budget violated")
	}
	for i := 0; i < 2000; i++ {
		s.Append(Entry{ServiceID: model.ServiceID(fmt.Sprintf("app/%d", i%20)), Text: strings.Repeat("x", 16384)})
	}
	bytes := 0
	for _, e := range s.Entries(nil) {
		bytes += len(e.Text) + 256
	}
	if bytes > 16*1024*1024 {
		t.Fatal("global budget exceeded")
	}
	bytes = 0
	for _, e := range s.Entries([]model.ServiceID{"app/0"}) {
		bytes += len(e.Text) + 256
	}
	if bytes > 2*1024*1024 {
		t.Fatal("service budget exceeded")
	}
}
func TestConsumeSplitCJKAndOversizeLine(t *testing.T) {
	out := []string{}
	err := Consume(context.Background(), strings.NewReader("教材\n"+strings.Repeat("长", 20000)+"\nafter\n"), func(s string) { out = append(out, s) })
	if err != nil || len(out) != 3 || out[0] != "教材" || out[2] != "after" || len(out[1]) > 16420 {
		t.Fatal(len(out), err)
	}
}
func TestSanitizeOSCAndControlSequences(t *testing.T) {
	for _, s := range []string{"\x1b]52;c;secret\x07教材", "\x1b[31m教材\x1b[0m", "\x1bPdanger\x1b\\教材", "教材\x00\x07"} {
		if got := Sanitize(s); got != "教材" {
			t.Fatalf("unsafe text %q", got)
		}
	}
}
