package main

import (
	"net/http"
	"strings"
)

func (a *app) clientLaunchMessage(message string) string {
	a.mu.Lock()
	language := a.config.Language
	a.mu.Unlock()
	if language != "es" {
		return message
	}
	known := map[string]string{
		"Xcode is available on macOS only.":         "Xcode solo está disponible en macOS.",
		"Unknown launch client.":                    "Cliente desconocido.",
		"Method not allowed.":                       "Método no permitido.",
		"Another launch is already being prepared.": "Ya se está preparando otra apertura.",
		"Cannot start the saved proxy. Check the Kilo credentials, organization and local port.": "No se pudo iniciar el proxy guardado. Revisa las credenciales de Kilo, la organización y el puerto local.",
		"Choose an absolute path to an existing project folder.":                                 "Indica la ruta absoluta de una carpeta de proyecto existente.",
		"The project folder does not exist or is not a directory.":                               "La carpeta del proyecto no existe o no es un directorio.",
		"A custom application path is supported only for Codex Desktop.":                         "La ruta de aplicación personalizada solo está disponible para Codex Desktop.",
		"Cannot create the isolated Codex window profile.":                                       "No se pudo crear el perfil de ventana independiente de Codex.",
		"The Codex application bundle is invalid.":                                               "El paquete de la aplicación Codex no es válido.",
		"Choose an absolute path to the Codex application.":                                      "Indica la ruta absoluta de la aplicación Codex.",
		"The selected Codex application does not exist or cannot be executed.":                   "La aplicación Codex seleccionada no existe o no se puede ejecutar.",
		"Start the Cursor HTTPS tunnel before launching Cursor.":                                 "Conecta el túnel HTTPS de Cursor antes de abrir Cursor.",
		"Zed opened. Local credentials and models are ready; existing projects stay open.":       "Zed abierto. Las credenciales locales y los modelos están preparados; tus proyectos siguen abiertos.",
		"Cursor opened. Connect its provider to the existing tunnel if needed.":                  "Cursor abierto. Conecta su proveedor al túnel existente si hace falta.",
		"Xcode opened. Existing projects stay open; complete its provider setup if needed.":      "Xcode abierto. Los proyectos existentes siguen abiertos; completa la configuración del proveedor si hace falta.",
		"Terminal is not installed":                                                              "Terminal no está instalado.",
		"A desktop session is required to open a terminal":                                       "Necesitas una sesión de escritorio para abrir una terminal.",
		"Install a desktop terminal to launch command-line clients":                              "Instala una terminal de escritorio para iniciar los clientes de terminal.",
	}
	if translated, ok := known[message]; ok {
		return translated
	}
	for _, id := range launchClients {
		name, _ := launchClientIdentity(id)
		patterns := map[string]string{
			"Install {client} on this computer, then refresh installed apps.": "Instala {client} en este equipo y actualiza las aplicaciones instaladas.",
			"{client} opened.": "{client} abierto.",
			"Could not open {client}. Check that the application and a terminal are available, then try again.":       "No se pudo abrir {client}. Comprueba que la aplicación y una terminal están disponibles e inténtalo de nuevo.",
			"Prepare {client} again: its saved profile is missing, unsafe or no longer matches the proxy connection.": "Vuelve a preparar {client}: su perfil guardado falta, no es seguro o ya no coincide con la conexión del proxy.",
		}
		for source, target := range patterns {
			if message == strings.ReplaceAll(source, "{client}", name) {
				return strings.ReplaceAll(target, "{client}", name)
			}
		}
	}
	return message
}
func (a *app) clientLaunchError(w http.ResponseWriter, status int, message string) {
	jsonError(w, status, a.clientLaunchMessage(message))
}
