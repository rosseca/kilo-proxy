package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"runtime"
	"strings"
	"time"
	"unicode"

	"github.com/gogpu/systray"
)

type trayState struct {
	status, team, address, activity string
	running, pending, configured    bool
}

func (a *app) trayState() trayState {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := trayState{
		running: a.proxyServer != nil, pending: a.authPending(),
		configured: a.apiKey != "" && a.config.OrgID != "",
		address:    fmt.Sprintf("127.0.0.1:%d", a.config.Port),
		activity:   fmt.Sprintf("%d en curso · %d peticiones · %d errores", a.active, a.requests, a.failures),
		team:       "Equipo: sin seleccionar", status: "○ Proxy detenido",
	}
	if a.config.OrgID != "" {
		name := a.config.OrgID
		for _, org := range a.organizations {
			if org.ID == a.config.OrgID {
				name = org.Name
				break
			}
		}
		s.team = "Equipo: " + menuText(name)
	}
	if s.running {
		s.status = "● Proxy activo"
	} else if s.pending {
		s.status = "○ Esperando login de Kilo…"
	} else if !s.configured {
		s.status = "○ Conexión pendiente de configurar"
	}
	return s
}

func menuText(s string) string {
	runes := []rune(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s))
	if len(runes) > 48 {
		return string(runes[:47]) + "…"
	}
	return string(runes)
}

// runTray owns the native event loop on the startup OS thread. Only the
// controller goroutine mutates menu items; systray dispatches those changes
// to the native thread. It finishes updating before removing the tray.
func (a *app) runTray(panelURL string) error {
	tray := systray.New()
	if err := tray.InitError(); err != nil {
		return err
	}
	tray.SetAppName("Kilo Local").SetTooltip("Kilo Local — conexión de tu equipo")
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(trayIcon(true))
	} else {
		tray.SetIcon(trayIcon(false))
	}
	actions := make(chan string, 8)
	dispatch := func(action string) func() {
		return func() {
			select {
			case actions <- action:
			default:
			}
		}
	}
	menu := systray.NewMenu()
	menu.Add("Kilo Local · "+version, nil).SetDisabled(true)
	status := menu.Add("Cargando…", nil)
	status.SetDisabled(true)
	team := menu.Add("Equipo: sin seleccionar", nil)
	team.SetDisabled(true)
	address := menu.Add("", nil)
	address.SetDisabled(true)
	activity := menu.Add("", nil)
	activity.SetDisabled(true)
	menu.AddSeparator()
	menu.Add("Abrir panel…", dispatch("open"))
	start := menu.Add("Arrancar proxy", dispatch("start"))
	stop := menu.Add("Detener proxy (cancela peticiones)", dispatch("stop"))
	const hint = "Cerrar el panel no detiene el proxy"
	message := menu.Add(hint, nil)
	message.SetDisabled(true)
	menu.AddSeparator()
	menu.Add("Salir de Kilo Local", a.requestQuit)
	tray.SetMenu(menu).OnDoubleClick(dispatch("open")).Show()

	finished := make(chan struct{})
	loopEnded := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		var last trayState
		update := func() {
			s := a.trayState()
			if s == last {
				return
			}
			status.SetLabel(s.status)
			team.SetLabel(s.team)
			address.SetLabel("Endpoint: " + s.address)
			activity.SetLabel(s.activity)
			if s.configured {
				start.SetLabel("Arrancar proxy")
			} else {
				start.SetLabel("Configurar conexión…")
			}
			start.SetDisabled(s.running || s.pending)
			stop.SetDisabled(!s.running)
			last = s
		}
		openPanel := func() {
			if err := openBrowser(panelURL); err != nil {
				message.SetLabel("No se pudo abrir el navegador")
				fmt.Fprintln(os.Stderr, "Open the control panel URL printed at startup.")
			}
		}
		update()
		for {
			select {
			case <-a.quit:
				tray.Remove()
				return
			case <-loopEnded:
				return
			case action := <-actions:
				message.SetLabel(hint)
				switch action {
				case "open":
					openPanel()
				case "start":
					if !a.trayState().configured {
						openPanel()
					} else if err := a.start(); err != nil {
						message.SetLabel("No se pudo arrancar; revisa el puerto en el panel")
						openPanel()
					}
				case "stop":
					a.stop()
				}
				update()
			case <-ticker.C:
				update()
			}
		}
	}()
	err := tray.Run()
	close(loopEnded)
	a.requestQuit()
	// An unexpected native loop error may leave a pending main-thread update.
	// Exit through main's server cleanup instead of waiting for that UI callback.
	if err != nil {
		return err
	}
	<-finished
	return err
}

// Rasterize our own K mark. macOS uses its alpha mask to adapt to light/dark
// menu bars; other desktops get a high-contrast badge on a transparent canvas.
func trayIcon(template bool) []byte {
	const size = 44
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px, py := float64(x)+.5, float64(y)+.5
			k := (px >= 10 && px < 16 && py >= 7 && py < 37) ||
				(px >= 15 && px < 34 && py >= 7 && py < 37 &&
					((py <= 23 && px+py >= 32 && px+py < 41) ||
						(py >= 21 && py-px >= -1 && py-px < 8)))
			if k {
				img.SetNRGBA(x, y, color.NRGBA{R: 30, G: 38, B: 32, A: 255})
			} else if !template && (px-22)*(px-22)+(py-22)*(py-22) <= 22*22 {
				img.SetNRGBA(x, y, color.NRGBA{R: 232, G: 243, B: 106, A: 255})
			}
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}
