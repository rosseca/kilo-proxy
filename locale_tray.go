package main

// trayLabels is independent of the native toolkit so every visible menu label,
// tooltip and status message follows the same persisted language as the window.
type trayLabels struct {
	tooltip, loading, teamEmpty, teamPrefix                                                                string
	stopped, running, pending, unconfigured, endpoint, activity                                            string
	open, start, setup, stop, hint, exit, openError, startError                                            string
	spend, subtotal, coverage                                                                              string
	balance, balanceTeam, balancePersonal, balanceUnavailable, balanceStale, balanceLoading, balanceSignIn string
}

func trayText(language string) trayLabels {
	if language == "es" {
		return trayLabels{
			tooltip: "Kilo Proxy — conexión de tu equipo", loading: "Cargando…",
			teamEmpty: "Equipo: sin seleccionar", teamPrefix: "Equipo: ",
			stopped: "○ Proxy detenido", running: "● Proxy activo", pending: "○ Esperando login de Kilo…",
			unconfigured: "○ Conexión pendiente de configurar", endpoint: "Endpoint: ",
			activity: "%d en curso · %d peticiones · %d errores",
			spend:    "Coste desde que abriste Kilo Proxy", subtotal: "Subtotal informado de esta sesión", coverage: "Coste informado: %d/%d peticiones",
			balance: "Saldo de Kilo", balanceTeam: "Saldo del equipo", balancePersonal: "Saldo personal", balanceUnavailable: "Saldo no disponible", balanceStale: "Saldo desactualizado; actualízalo en Actividad", balanceLoading: "Actualizando saldo…", balanceSignIn: "Inicia sesión para consultar el saldo",
			open: "Abrir Kilo Proxy…", start: "Arrancar proxy", setup: "Configurar conexión…",
			stop: "Detener proxy (cancela peticiones)", hint: "Cerrar la ventana no detiene el proxy", exit: "Salir de Kilo Proxy",
			openError: "No se pudo abrir Kilo Proxy", startError: "No se pudo arrancar; consulta el error en la ventana",
		}
	}
	return trayLabels{
		tooltip: "Kilo Proxy — your team connection", loading: "Loading…",
		teamEmpty: "Team: not selected", teamPrefix: "Team: ",
		stopped: "○ Proxy stopped", running: "● Proxy running", pending: "○ Waiting for Kilo login…",
		unconfigured: "○ Connection needs setup", endpoint: "Endpoint: ",
		activity: "%d active · %d requests · %d errors",
		spend:    "Cost since opening Kilo Proxy", subtotal: "Reported subtotal this session", coverage: "Cost reported: %d/%d requests",
		balance: "Kilo balance", balanceTeam: "Team balance", balancePersonal: "Personal balance", balanceUnavailable: "Balance unavailable", balanceStale: "Balance out of date; refresh in Activity", balanceLoading: "Refreshing balance…", balanceSignIn: "Sign in to check your balance",
		open: "Open Kilo Proxy…", start: "Start proxy", setup: "Set up connection…",
		stop: "Stop proxy (cancels requests)", hint: "Closing the window keeps the proxy running", exit: "Quit Kilo Proxy",
		openError: "Could not open Kilo Proxy", startError: "Could not start; see the error in the window",
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
