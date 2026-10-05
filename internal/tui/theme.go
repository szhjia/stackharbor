package tui

import (
	lip "charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/szhjia/stackharbor/internal/logs"
	"os"
	"strings"
)

func fit(s string, w int) string {
	if w < 1 {
		return ""
	}
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}
func plain(s string) string { return logs.Sanitize(s) }

const (
	accent = "#73BCE8"
	muted  = "#A7B4C2"
	green  = "#75C6A4"
	amber  = "#E8BB71"
	red    = "#E97676"
)

func noColor() bool {
	_, disabled := os.LookupEnv("NO_COLOR")
	return disabled || os.Getenv("TERM") == "dumb"
}

func (m Model) palette(color string) string {
	if m.lightTheme {
		switch color {
		case green:
			return "#26704D"
		case amber:
			return "#805A12"
		case red:
			return "#AA3038"
		case accent:
			return "#1E6091"
		case muted:
			return "#536372"
		case "#344D63":
			return "#DCEAF5"
		case "#E8F3FC":
			return "#184B73"
		case "#27313D":
			return "#F0F2F4"
		case "#E2E8F0":
			return "#27313D"
		}
	}
	// Bubble Tea converts these colors to the terminal's supported profile.
	return color
}

func (m Model) tone(s, color string) string {
	if noColor() {
		return s
	}
	return lip.NewStyle().Foreground(lip.Color(m.palette(color))).Render(s)
}
func heading(s string) string {
	if noColor() {
		return s
	}
	return lip.NewStyle().Bold(true).Render(s)
}
func (m Model) selectedRow(s string, width int) string {
	s = fit(s, width)
	if noColor() {
		return s
	}
	return paintRow(s, width, uv.Style{Fg: lip.Color(m.palette("#E8F3FC")), Bg: lip.Color(m.palette("#344D63"))}, true)
}

// Paint the sidebar as a bounded cell region. Nested ANSI resets cannot erase
// its whitespace or carry a background into the divider and content pane.
func (m Model) sidebarBackground(s string, width int) string {
	style := uv.Style{Fg: lip.Color(m.palette("#E2E8F0")), Bg: lip.Color(m.palette("#27313D"))}
	return paintRow(s, width, style, false)
}

func paintRow(s string, width int, style uv.Style, overwriteBackground bool) string {
	s = fit(s, width)
	if noColor() || width < 1 {
		return s
	}
	screen := uv.NewScreenBuffer(width, 1)
	screen.Method = ansi.GraphemeWidth
	uv.NewStyledString(s).Draw(screen, uv.Rect(0, 0, width, 1))
	for i := range screen.Lines[0] {
		c := &screen.Lines[0][i]
		if c.Width == 0 {
			continue
		}
		if c.Style.Bg == nil || overwriteBackground {
			c.Style.Bg = style.Bg
		}
		if style.Fg != nil && (overwriteBackground || c.Style.Fg == nil) {
			c.Style.Fg = style.Fg
		}
	}
	return screen.Lines[0].Render()
}
func (m Model) keycap(s string) string {
	if noColor() {
		return s
	}
	return lip.NewStyle().Foreground(lip.Color(m.palette(accent))).Bold(true).Render(s)
}
func stateSymbol(s string) string {
	switch s {
	case "succeeded", "available", "started", "running":
		return "●"
	case "unknown", "failed":
		return "!"
	case "blocked", "checking", "verifying", "running-task", "starting", "waiting", "stopping", "unready":
		return "◐"
	default:
		return "-"
	}
}
func (m Model) stateBadge(s string) string {
	return m.tone(stateSymbol(s)+" "+stateLabel(s), stateColor(s))
}
func stateColor(s string) string {
	switch s {
	case "succeeded", "available", "started", "running":
		return "#75C6A4"
	case "unknown", "failed":
		return red
	case "blocked", "checking", "verifying", "running-task", "unready", "stopping", "starting", "waiting":
		return "#E8BB71"
	default:
		return muted
	}
}
func stateLabel(s string) string {
	switch s {
	case "blocked":
		return "Blocked"
	case "unavailable":
		return "Unavailable"
	case "unhealthy":
		return "Unhealthy"
	case "checking":
		return "Checking"
	case "verifying":
		return "Verifying"
	case "running-task":
		return "Executing"
	case "succeeded":
		return "Completed"
	case "available":
		return "Available"
	case "started":
		return "Alive"
	case "unknown":
		return "Unknown"
	case "running":
		return "Running"
	case "stopped":
		return "Stopped"
	case "failed":
		return "Failed"
	case "unready":
		return "Not ready"
	case "starting":
		return "Starting"
	case "waiting":
		return "Waiting"
	case "stopping":
		return "Stopping"
	}
	return s
}
