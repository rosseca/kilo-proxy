//go:build desktop

package main

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/font"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

// uxhGroup is one titled list of harnesses on the Harnesses page.
type uxhGroup struct {
	en, es, captionEN, captionES string
	keys                         []string
}

// uxhGroups orders the harnesses as the redesign groups them. A harness that
// is not installed moves to the "Not installed" group at the end.
func (u *nativeUI) uxhGroups() []uxhGroup {
	folder := uxhShortPath(u.uxhGlobalFolder())
	return []uxhGroup{
		{"Desktop apps", "Apps de escritorio", "The official apps, with a separate Kilo profile", "Las apps oficiales, con un perfil de Kilo aparte", []string{"codex", "claude-desktop"}},
		{"Multi-agent", "Multiagente", "Several Codex and Claude agents in one workspace", "Varios agentes Codex y Claude en un mismo espacio", []string{"synara", "t3-code"}},
		{"Bots", "Bots", "Assistants that work on their own", "Asistentes que trabajan por su cuenta", []string{"openmausbot"}},
		{"Coding harnesses", "Harnesses de código", "Terminal and editors · open in " + folder, "Terminal y editores · se abren en " + folder, []string{"claude", "codex-cli", "opencode", "zed", "open-design", "omp"}},
	}
}

// uxhName is the short product name shown in the redesign.
func uxhName(key string) string {
	switch key {
	case "codex":
		return "Codex"
	case "claude-desktop":
		return "Claude Desktop"
	case "t3-code":
		return "T3 Code"
	case "synara":
		return "Synara"
	case "generic":
		return "API"
	}
	name, _ := launchClientIdentity(key)
	return name
}

func uxhMonogramText(key string) string {
	return map[string]string{
		"codex": "CX", "claude-desktop": "CD", "synara": "SY", "t3-code": "T3", "openmausbot": "MB",
		"claude": ">CC", "codex-cli": ">CX", "opencode": ">OC", "zed": "ZD", "open-design": "OD", "omp": ">PI",
		"xcode": "XC", "generic": "API",
	}[key]
}

// uxhInstallURL is where a harness's installation guidance lives.
func (u *nativeUI) uxhInstallURL(key string) string {
	if url := u.clientState().LaunchInfo.Clients[key].InstallURL; url != "" {
		return url
	}
	if url := launchClientInstallURL(key); url != "" {
		return url
	}
	switch key {
	case "codex":
		return "https://openai.com/codex/"
	case "claude-desktop":
		return "https://claude.ai/download"
	case "zed":
		return "https://zed.dev/download"
	case "omp":
		return "https://omp.sh/"
	case "open-design":
		return u.openDesignInstallURL()
	}
	return ""
}

// uxhShortPath abbreviates the home directory as ~ for display.
func uxhShortPath(path string) string {
	if path == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if path == home {
			return "~"
		}
		if rel, err := filepath.Rel(home, path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return "~" + string(filepath.Separator) + rel
		}
	}
	return path
}

// uxhAgo describes how long ago something happened, in the user's language.
func (u *nativeUI) uxhAgo(at, now time.Time) string {
	elapsed := now.Sub(at)
	switch {
	case elapsed < 2*time.Minute:
		return u.tr("now", "ahora")
	case elapsed < time.Hour:
		return fmt.Sprintf(u.tr("%d min ago", "hace %d min"), int(elapsed/time.Minute))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf(u.tr("%d h ago", "hace %d h"), int(elapsed/time.Hour))
	case elapsed < 48*time.Hour:
		return u.tr("yesterday", "ayer")
	case elapsed < 60*24*time.Hour:
		return fmt.Sprintf(u.tr("%d days ago", "hace %d días"), int(elapsed/(24*time.Hour)))
	}
	return at.Format("2006-01-02")
}

// uxhLastUsed is the "last used" caption of a harness, or "" when unknown.
func (u *nativeUI) uxhLastUsed(key string) string {
	a := u.agentsState()
	at, ok := a.Preferences.LastOpened[key]
	if !ok || at <= 0 {
		return ""
	}
	when, now := time.Unix(at, 0), a.now()
	elapsed := now.Sub(when)
	switch {
	case elapsed < 2*time.Minute:
		return u.tr("Opened just now", "Abierto ahora")
	case elapsed >= 24*time.Hour && elapsed < 48*time.Hour:
		return u.tr("Yesterday", "Ayer")
	}
	return u.tr("Used ", "Usado ") + u.uxhAgo(when, now)
}

// uxhModelsSummary describes the shared library a harness opens with.
func (u *nativeUI) uxhModelsSummary() string {
	u.initModelLibrary()
	s := u.library.selection
	if len(s.Models) == 0 {
		return u.tr("No models yet", "Aún no hay modelos")
	}
	name := s.Initial
	if choice := s.choice(s.Initial); choice != nil {
		name = nativeCodexDisplayName(*choice)
	}
	if len(s.Models) == 1 {
		return fmt.Sprintf(u.tr("1 model · starts with %s", "1 modelo · empieza con %s"), name)
	}
	return fmt.Sprintf(u.tr("%d models · starts with %s", "%d modelos · empieza con %s"), len(s.Models), name)
}

// uxhMonogram is a harness's square badge: the installed app's own icon when
// it can be read, otherwise solid black when installed and a dashed grey
// outline when it is not.
func (u *nativeUI) uxhMonogram(key string, size unit.Dp, installed bool) layout.Widget {
	if installed {
		if icon, ok := u.uxhAppIcon(key); ok {
			return u.uxhAppIconLayout(key, icon, size)
		}
	}
	return func(gtx layout.Context) layout.Dimensions {
		side := gtx.Dp(size)
		box := image.Pt(side, side)
		textColor := nativeSurface
		if installed {
			paint.FillShape(gtx.Ops, nativeInk, clip.Rect{Max: box}.Op())
		} else {
			textColor = nativeTextDisabled
			paint.FillShape(gtx.Ops, nativeSurface, clip.Rect{Max: box}.Op())
			uxhDashedBorder(gtx, box, gtx.Dp(2), nativeTextDisabled)
		}
		gtx.Constraints = layout.Exact(box)
		textSize := unit.Sp(float32(size) * 0.36)
		layout.Center.Layout(gtx, u.monoStyle(textSize, uxhMonogramText(key), textColor, font.Bold))
		return layout.Dimensions{Size: box}
	}
}

func uxhDashedBorder(gtx layout.Context, box image.Point, width int, c color.NRGBA) {
	dash, gap := gtx.Dp(5), gtx.Dp(3)
	for x := 0; x < box.X; x += dash + gap {
		end := min(x+dash, box.X)
		paint.FillShape(gtx.Ops, c, clip.Rect{Min: image.Pt(x, 0), Max: image.Pt(end, width)}.Op())
		paint.FillShape(gtx.Ops, c, clip.Rect{Min: image.Pt(x, box.Y-width), Max: image.Pt(end, box.Y)}.Op())
	}
	for y := 0; y < box.Y; y += dash + gap {
		end := min(y+dash, box.Y)
		paint.FillShape(gtx.Ops, c, clip.Rect{Min: image.Pt(0, y), Max: image.Pt(width, end)}.Op())
		paint.FillShape(gtx.Ops, c, clip.Rect{Min: image.Pt(box.X-width, y), Max: image.Pt(box.X, end)}.Op())
	}
}

// uxhSquareButton is an icon-only square control with an accessible label.
func (u *nativeUI) uxhSquareButton(id, label string, icon *widget.Icon, action func()) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b := u.clickable(id)
		for b.Clicked(gtx) {
			if gtx.Enabled() && action != nil {
				action()
				if u.invalidate != nil {
					u.invalidate()
				}
			}
		}
		side := gtx.Dp(44)
		box := image.Pt(side, side)
		return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.ClassOp(semantic.Button).Add(gtx.Ops)
			semantic.LabelOp(label).Add(gtx.Ops)
			background, foreground := nativeSurface, nativeInk
			if b.Hovered() && gtx.Enabled() {
				background = nativeIvory
			}
			if !gtx.Enabled() {
				background, foreground = nativeDisabledBG, nativeTextDisabled
			}
			paint.FillShape(gtx.Ops, background, clip.Rect{Max: box}.Op())
			gtx.Constraints = layout.Exact(box)
			widget.Border{Color: nativeInk, Width: nativeLine}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min = image.Pt(gtx.Dp(20), gtx.Dp(20))
					return icon.Layout(gtx, foreground)
				})
			})
			return layout.Dimensions{Size: box}
		})
	}
}

// uxhStatus is the coloured installation line: "● Installed", "○ Not installed".
func (u *nativeUI) uxhStatus(state uxhInstall, detail string) layout.Widget {
	text, c := u.tr("Checking…", "Comprobando…"), nativeTextSubtle
	switch state {
	case uxhInstalled:
		text, c = "● "+u.tr("Installed", "Instalado"), nativeSuccessFG
	case uxhMissing:
		text = "○ " + u.tr("Not installed", "No instalado")
	}
	if detail != "" && state == uxhInstalled {
		text += " · " + detail
	}
	return u.textStyle(13, text, c, font.Medium)
}
