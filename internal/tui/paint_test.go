package tui

import (
	"fmt"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/szhjia/stackharbor/internal/model"
	"os"
	"strings"
	"testing"
)

// Catch edge-to-edge selection, nested status resets, and unpainted rows in
// the narrow navigation view using the same cell stream the terminal receives.
func TestSidebarPaddingAndSelectionBounds(t *testing.T) {
	old, ok := os.LookupEnv("NO_COLOR")
	os.Unsetenv("NO_COLOR")
	t.Cleanup(func() {
		if ok {
			os.Setenv("NO_COLOR", old)
		}
	})
	t.Setenv("TERM", "xterm-256color")
	for _, colorMode := range []string{"", "truecolor"} {
		t.Setenv("COLORTERM", colorMode)
		for _, width := range []int{60, 80, 120} {
			for _, selected := range []int{0, 1, 2} {
				t.Run(fmt.Sprintf("%s/%d/selected%d", colorMode, width, selected), func(t *testing.T) {
					f := designFixture()
					f.snapshot.Projects[0].Name = "业务 API（含 OCR）"
					m := resized(NewModel(f), width, 24)
					m.selected, m.showList = selected, true
					if selected == 2 {
						m.service = 0
					}
					screen := uv.NewScreenBuffer(width, 24)
					screen.Method = ansi.GraphemeWidth
					uv.NewStyledString(m.render()).Draw(screen, uv.Rect(0, 0, width, 24))
					right := 30
					if width == 80 {
						right = 26
					} else if width == 60 {
						right = 58
					}
					background := screen.Lines[20][2].Style.Bg
					if background == nil {
						t.Fatal("sidebar bottom row has no background")
					}
					selectionRows := 0
					for y := 3; y < 21; y++ {
						selection := false
						for x := 2; x < right; x++ {
							c := screen.Lines[y][x]
							if c.Width == 0 {
								continue
							}
							if c.Style.Bg == nil {
								t.Errorf("missing background at %d,%d", x, y)
							}
							inset := y == 3 || y == 20 || x < 4 || x >= right-2
							if inset && (c.Content != " " || c.Style.Bg != background) {
								t.Errorf("sidebar padding occupied at %d,%d: %#v", x, y, c)
							}
							if c.Style.Bg != nil && c.Style.Bg != background {
								selection = true
							}
						}
						if selection {
							selectionRows++
							bg := screen.Lines[y][4].Style.Bg
							for x := 4; x < right-2; x++ {
								c := screen.Lines[y][x]
								if c.Width > 0 && c.Style.Bg != bg {
									t.Errorf("selection interrupted at %d,%d", x, y)
								}
							}
						}
						for x := right; x < width; x++ {
							if screen.Lines[y][x].Style.Bg != nil {
								t.Errorf("sidebar background leaks at %d,%d", x, y)
								break
							}
						}
					}
					wantRows := []int{1, 2, 3}[selected]
					if selectionRows != wantRows {
						t.Errorf("selected rows = %d, want %d", selectionRows, wantRows)
					}
				})
			}
		}
	}
}

func TestBackgroundConfinedToSidebar(t *testing.T) {
	old, ok := os.LookupEnv("NO_COLOR")
	os.Unsetenv("NO_COLOR")
	t.Cleanup(func() {
		if ok {
			os.Setenv("NO_COLOR", old)
		}
	})
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")
	for _, mode := range []string{"overview", "project", "help"} {
		m := resized(NewModel(designFixture()), 120, 24)
		if mode == "project" {
			m.selected = 2
		}
		if mode == "help" {
			m.help = true
		}
		screen := uv.NewScreenBuffer(m.width, m.height)
		screen.Method = ansi.GraphemeWidth
		uv.NewStyledString(m.render()).Draw(screen, uv.Rect(0, 0, m.width, m.height))
		rows := screen.Lines
		for y, row := range rows {
			for x, c := range row {
				if c.Width == 0 {
					continue
				}
				should := y >= 3 && y < 21 && x >= 2 && x < 30 && mode != "help"
				if should && c.Style.Bg == nil {
					t.Errorf("%s missing sidebar background at %d,%d", mode, x, y)
				}
				if !should && c.Style.Bg != nil {
					t.Errorf("%s background leaks at %d,%d", mode, x, y)
					break
				}
			}
		}
	}
}
func TestFooterNameBeforeKeyAndSpacing(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := resized(NewModel(designFixture()), 180, 24)
	footer := m.footer()
	rows := strings.Split(m.render(), "\n")
	if !strings.HasPrefix(rows[len(rows)-2], "  ") {
		t.Fatal("left inset missing")
	}
	if !strings.Contains(footer, "Start all Shift+S   Stop all Shift+X") {
		t.Fatal("label/key order or spacing wrong", footer)
	}
}

func TestSidebarCompactNavigationAndReadableIdleStatus(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := resized(NewModel(designFixture()), 120, 24)
	m.selected = 1
	rows := m.sidebar(28, 18)
	if strings.TrimSpace(rows[0]) != "" || strings.TrimSpace(rows[17]) != "" {
		t.Fatal("sidebar lost vertical padding")
	}
	want := []string{"Dashboard", "", "› Admin console", "- Not started", "", "PageSmith backend"}
	for i, text := range want {
		if got := strings.TrimSpace(rows[i+1]); got != text {
			t.Errorf("navigation row %d = %q, want %q", i+1, got, text)
		}
	}
}

func TestFooterUsesArrowHintAndLabelsItsOwnMetrics(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	f := designFixture()
	toolCPU := 1.5
	f.snapshot.Tool = model.Metric{Known: true, RSS: 21 * 1048576, CPUPercent: &toolCPU}
	f.snapshot.Services[0].Metric = model.Metric{Known: true, RSS: 128 * 1048576}
	m := resized(NewModel(f), 180, 24)
	footer := m.footer()
	if !strings.Contains(footer, "Select ↑/↓   Start all Shift+S") {
		t.Fatal("footer lost compact, grouped arrow shortcut", footer)
	}
	if !strings.Contains(footer, "StackHarbor memory 21.0 MiB · CPU 1.5%") || strings.Contains(footer, "128.0 MiB") {
		t.Fatal("footer does not distinguish tool metrics from application metrics", footer)
	}
}

func TestNarrowProjectKeepsObservedResourceValues(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	f := designFixture()
	cpu := 12.5
	f.snapshot.Services[1].Metric = model.Metric{Known: true, RSS: 128 * 1048576, CPUPercent: &cpu}
	f.snapshot.Services[1].MetricSource = "external"
	m := resized(NewModel(f), 80, 24)
	m.selected = 2
	view := m.render()
	if !strings.Contains(view, "128.0 MiB") || !strings.Contains(view, "12.5%") {
		t.Fatal("resource values hidden on narrow project page", view)
	}
}

func TestEveryScreenSharesOuterInsets(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, mode := range []string{"overview", "project", "help", "confirm", "docker"} {
		m := resized(NewModel(designFixture()), 120, 24)
		switch mode {
		case "project":
			m.selected = 2
		case "help":
			m.help = true
		case "confirm":
			m.confirm = true
		case "docker":
			m.dockerView = true
		}
		rows := strings.Split(m.render(), "\n")
		if len(rows) != 24 {
			t.Fatal("wrong page height", mode, len(rows))
		}
		if strings.TrimSpace(rows[0]) != "" || strings.TrimSpace(rows[23]) != "" {
			t.Fatal("vertical inset missing", mode)
		}
		for _, row := range rows {
			if !strings.HasPrefix(row, "  ") || !strings.HasSuffix(row, "  ") || ansi.StringWidth(row) != 120 {
				t.Fatal("inconsistent horizontal inset", mode, row)
			}
		}
	}
}
