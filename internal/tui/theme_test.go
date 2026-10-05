package tui

import (
	"image/color"
	"math"
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func luminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	linear := func(v uint32) float64 {
		n := float64(v) / 65535
		if n <= 0.04045 {
			return n / 12.92
		}
		return math.Pow((n+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(r) + 0.7152*linear(g) + 0.0722*linear(b)
}

func TestTerminalBackgroundSelectsReadableSidebarTheme(t *testing.T) {
	old, ok := os.LookupEnv("NO_COLOR")
	os.Unsetenv("NO_COLOR")
	t.Cleanup(func() {
		if ok {
			os.Setenv("NO_COLOR", old)
		}
	})
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")
	var backgrounds []color.Color
	for _, terminal := range []color.Color{color.White, color.Black} {
		m := resized(NewModel(designFixture()), 120, 24)
		v, _ := m.Update(tea.BackgroundColorMsg{Color: terminal})
		m = v.(Model)
		m.selected = 1
		screen := uv.NewScreenBuffer(120, 24)
		screen.Method = ansi.GraphemeWidth
		uv.NewStyledString(m.render()).Draw(screen, uv.Rect(0, 0, 120, 24))
		bg := screen.Lines[3][2].Style.Bg
		backgrounds = append(backgrounds, bg)
		if bg == nil {
			t.Fatal("sidebar background missing")
		}
		for y := 3; y < 21; y++ {
			for x := 2; x < 30; x++ {
				c := screen.Lines[y][x]
				if c.Width == 0 || c.Content == " " {
					continue
				}
				if c.Style.Fg == nil {
					t.Fatalf("sidebar text has no theme color at %d,%d", x, y)
				}
				a, b := luminance(c.Style.Fg), luminance(c.Style.Bg)
				contrast := (max(a, b) + 0.05) / (min(a, b) + 0.05)
				if contrast < 4.5 {
					t.Errorf("sidebar text contrast %.2f < 4.5 at %d,%d", contrast, x, y)
				}
			}
		}
	}
	if luminance(backgrounds[0]) < 0.8 {
		t.Error("light terminal sidebar is too dark")
	}
	if luminance(backgrounds[1]) < 0.02 || luminance(backgrounds[1]) > 0.1 {
		t.Error("dark terminal sidebar should be a softer dark shade")
	}
}
