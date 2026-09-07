package main

// trayLabels is independent of the native toolkit so every visible menu label,
// tooltip and status message follows the same persisted language as the window.
type trayLabels struct {
	tooltip, loading, teamEmpty, teamPrefix                     string
	stopped, running, pending, unconfigured, endpoint, activity string
	open, start, setup, stop, hint, exit, openError, startError string
}

func trayText(language string) trayLabels {
	if language == "es" {
		return trayLabels{
			tooltip: "Kilo Local — conexión de tu equipo", loading: "Cargando…",
			teamEmpty: "Equipo: sin seleccionar", teamPrefix: "Equipo: ",
			stopped: "○ Proxy detenido", running: "● Proxy activo", pending: "○ Esperando login de Kilo…",
			unconfigured: "○ Conexión pendiente de configurar", endpoint: "Endpoint: ",
			activity: "%d en curso · %d peticiones · %d errores",
			open:     "Abrir Kilo Local…", start: "Arrancar proxy", setup: "Configurar conexión…",
			stop: "Detener proxy (cancela peticiones)", hint: "Cerrar la ventana no detiene el proxy", exit: "Salir de Kilo Local",
			openError: "No se pudo abrir Kilo Local", startError: "No se pudo arrancar; revisa el puerto en la ventana",
		}
	}
	return trayLabels{
		tooltip: "Kilo Local — your team connection", loading: "Loading…",
		teamEmpty: "Team: not selected", teamPrefix: "Team: ",
		stopped: "○ Proxy stopped", running: "● Proxy running", pending: "○ Waiting for Kilo login…",
		unconfigured: "○ Connection needs setup", endpoint: "Endpoint: ",
		activity: "%d active · %d requests · %d errors",
		open:     "Open Kilo Local…", start: "Start proxy", setup: "Set up connection…",
		stop: "Stop proxy (cancels requests)", hint: "Closing the window keeps the proxy running", exit: "Quit Kilo Local",
		openError: "Could not open Kilo Local", startError: "Could not start; check the port in the window",
	}
}

func (labels trayLabels) message(kind string) string {
	switch kind {
	case "open-error":
		return labels.openError
	case "start-error":
		return labels.startError
	default:
		return labels.hint
	}
}
