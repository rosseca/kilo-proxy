package main

import (
	"regexp"
	"strings"
)

var nativeGatewayHTTP = regexp.MustCompile(`^Kilo devolvió HTTP (\d+)\. Revisa la clave y la organización\.$`)
var nativeGatewayHTTPEnglish = regexp.MustCompile(`^Kilo returned HTTP (\d+)\. Check your key and organization\.$`)

// Translate only known application messages and anchored application prefixes.
// Unknown error details, URLs, credentials and user data remain untouched.
func nativeMessage(message, language string) string {
	if language == "es" {
		if match := nativeGatewayHTTPEnglish.FindStringSubmatch(message); match != nil {
			return "Kilo devolvió HTTP " + match[1] + ". Revisa la clave y la organización."
		}
		for spanish, english := range nativeBackendMessages {
			if message == english {
				return spanish
			}
		}
		return message
	}
	if match := nativeGatewayHTTP.FindStringSubmatch(message); match != nil {
		return "Kilo returned HTTP " + match[1] + ". Check your key and organization."
	}
	if translated, ok := nativeBackendMessages[message]; ok {
		return translated
	}
	const schemaPrefix = "No se pudo adaptar el esquema de herramientas para Anthropic: "
	if strings.HasPrefix(message, schemaPrefix) {
		return "Could not adapt the tool schema for Anthropic: " + strings.TrimPrefix(message, schemaPrefix)
	}
	return message
}

// Canonical backend translations shared with ui/i18n.mjs.
var nativeBackendMessages = map[string]string{
	"Elige entre 1 y 50 modelos.": "Choose 1–50 models.",
	"Elige IDs reales de modelos con nombres válidos; Desktop no admite límites de contexto o salida personalizados.":                        "Choose real model IDs with valid names; Desktop context/output overrides are not supported.",
	"Los modelos experimentales de Claude Desktop están desactivados. Actívalos o prepara un perfil que solo contenga modelos Claude.":       "Experimental Claude Desktop models are disabled. Enable them or prepare a profile containing only Claude models.",
	"Indica experimentalModels como un booleano JSON.":                                                                                       "Provide experimentalModels as a JSON boolean.",
	"No se pudo acceder con seguridad a la carpeta de ajustes de Kilo.":                                                                      "Cannot safely access the Kilo settings directory.",
	"No se pudieron guardar con seguridad los ajustes de Kilo.":                                                                              "Cannot safely write Kilo settings.",
	"No se pudieron guardar las opciones de Claude Desktop. Revisa los permisos de la carpeta de ajustes.":                                   "Could not save Claude Desktop options. Check the settings directory permissions.",
	"Claude Desktop abierto con su configuración de gateway de Kilo.":                                                                        "Claude Desktop opened with its Kilo gateway configuration.",
	"Instala Claude Desktop y actualiza las aplicaciones instaladas. Claude Code CLI es una aplicación distinta.":                            "Install Claude Desktop, then refresh installed apps. Claude Code CLI is a separate application.",
	"No se pudieron comprobar las aplicaciones de escritorio abiertas.":                                                                      "Cannot inspect running desktop applications.",
	"No se pudo comprobar si Claude Desktop está abierto. Cierra Claude Desktop y vuelve a intentarlo.":                                      "Cannot check whether Claude Desktop is running. Quit Claude Desktop and try again.",
	"Cierra Claude Desktop y ábrelo desde aquí para cargar la configuración de Kilo. Las sesiones existentes no se cierran automáticamente.": "Quit Claude Desktop, then open it here to load the Kilo configuration. Existing sessions are not closed automatically.",
	"Elige entre 1 y 50 modelos Claude.":                                                                                                     "Choose 1–50 Claude models.",
	"Elige IDs reales de modelos Claude con nombres válidos; Desktop no admite límites de contexto o salida personalizados.":                 "Choose real Claude model IDs with valid names; Desktop context/output overrides are not supported.",
	"No se pudo localizar la configuración de Claude Desktop.":                                                                               "Cannot locate Claude Desktop settings.",
	"Sistema operativo no compatible con Claude Desktop.":                                                                                    "Unsupported Claude Desktop platform.",
	"La configuración de Claude Desktop debe contener objetos JSON con claves únicas; no se guardó nada.":                                    "Claude Desktop settings must be JSON objects with unique keys; nothing saved.",
	"La configuración de Claude Desktop no es válida; no se guardó nada.":                                                                    "Invalid Claude Desktop settings; nothing saved.",
	"No se pudo leer con seguridad la configuración de Claude Desktop; no se guardó nada.":                                                   "Cannot safely read Claude Desktop settings; nothing saved.",
	"La configuración de Claude Desktop supera el tamaño permitido.":                                                                         "Claude Desktop settings exceed the size limit.",
	"No se pudieron comprobar los permisos del perfil de Claude Desktop.":                                                                    "Cannot inspect Claude Desktop profile permissions.",
	"La copia del perfil de Claude Desktop no es segura; no se guardó nada.":                                                                 "Unsafe Claude Desktop profile backup; nothing saved.",
	"Los metadatos de la biblioteca de configuraciones de Claude Desktop no son válidos; no se guardó nada.":                                 "Invalid Claude Desktop config library metadata; nothing saved.",
	"Las configuraciones de Claude Desktop deben tener UUID únicos y nombres válidos; no se guardó nada.":                                    "Claude Desktop config library entries must have unique UUIDs and names; nothing saved.",
	"El UUID de la configuración activa de Claude Desktop no es válido; no se guardó nada.":                                                  "Invalid active Claude Desktop config UUID; nothing saved.",
	"No se pudieron comprobar las preferencias administradas de Claude Desktop.":                                                             "Cannot inspect managed Claude Desktop preferences.",
	"Las preferencias administradas de Claude Desktop no son válidas.":                                                                       "Invalid managed Claude Desktop preferences.",
	"No se pudo comprobar la configuración administrada de Claude Desktop.":                                                                  "Cannot inspect managed Claude Desktop settings.",
	"Tu organización administra la inferencia de Claude Desktop; los perfiles locales no se aplicarían.":                                     "Claude Desktop inference is managed by your organization; local profiles would be ignored.",
	"No se pudo comprobar con seguridad la configuración administrada de Claude Desktop.":                                                    "Cannot safely inspect managed Claude Desktop settings.",
	"No se pudo comprobar la configuración administrada de Claude Desktop; el perfil local no se modificó.":                                  "Cannot inspect managed Claude Desktop settings; local profile not changed.",
	"La carpeta del perfil de Kilo no es segura.":                                                                                            "Unsafe Kilo profile directory.",
	"No hay una selección de modelos de Claude Desktop guardada.":                                                                            "No saved Claude Desktop model selection.",
	"La selección guardada de modelos de Claude Desktop no es válida.":                                                                       "Invalid saved Claude Desktop model selection.",
	"Prepara primero el perfil de Claude Desktop; su carpeta de configuración falta o no es segura.":                                         "Prepare the Claude Desktop profile first; its settings directory is missing or unsafe.",
	"Prepara primero el perfil de Claude Desktop; su configuración falta o no es válida.":                                                    "Prepare the Claude Desktop profile first; its configuration is missing or invalid.",
	"La configuración del gateway de Claude Desktop ha cambiado; vuelve a preparar el perfil.":                                               "Claude Desktop gateway settings changed; prepare the profile again.",
	"La autenticación del gateway de Claude Desktop ha cambiado; vuelve a preparar el perfil.":                                               "Claude Desktop gateway authentication changed; prepare the profile again.",
	"La configuración Kilo de Claude Desktop no está activa; vuelve a preparar el perfil.":                                                   "The Kilo Claude Desktop configuration is not active; prepare the profile again.",
	"El modo de terceros de Claude Desktop no está activo; vuelve a preparar el perfil.":                                                     "Claude Desktop third-party mode is not active; prepare the profile again.",
	"La selección guardada de Claude Desktop no es válida; no se guardó nada.":                                                               "Invalid saved Claude Desktop selection; nothing saved.",
	"No se pudo leer con seguridad la selección guardada de Claude Desktop; no se guardó nada.":                                              "Cannot safely read saved Claude Desktop selection; nothing saved.",
	"El JSON de configuración de Claude Desktop no es válido.":                                                                               "Invalid Claude Desktop setup JSON.",
	"Elige un modelo inicial de la selección.":                                                                                               "Choose an initial model from the selection.",

	"Elige high, balanced o small para la compresión de imágenes.":                   "Choose high, balanced, or small for image compression.",
	"Espera a que termine el login para cargar los modelos.":                         "Wait for login to finish before loading models.",
	"El catálogo de Kilo supera el tamaño permitido.":                                "The Kilo catalog exceeds the size limit.",
	"La conexión ha cambiado. Vuelve a cargar los modelos.":                          "The connection changed. Reload the models.",
	"Kilo devolvió un catálogo no válido.":                                           "Kilo returned an invalid catalog.",
	"Este enlace ha caducado":                                                        "This link has expired",
	"No se pudo completar la operación.":                                             "The operation could not be completed.",
	"Copiado al portapapeles":                                                        "Copied to clipboard",
	"Autoriza este código en la página de Kilo. Esperando a que completes el login…": "Authorize this code on Kilo's website. Waiting for you to finish signing in…",
	"Login cancelado.": "Sign-in cancelled.",
	"introduce tu API key y el ID de organización":                "Enter your API key and organization ID",
	"Se requiere JSON.":                                           "JSON is required.",
	"Petición JSON no válida.":                                    "Invalid JSON request.",
	"Origen no permitido.":                                        "Origin not allowed.",
	"Abre el panel desde la aplicación para recuperar el acceso.": "Open the panel from the application to regain access.",
	"Método no permitido.":                                        "Method not allowed.",
	"No se pudo abrir el puerto. Puede estar ocupado por otra instancia; elige otro puerto.":                               "Could not open the port. Another instance may be using it; choose another port.",
	"El puerto debe estar entre 1024 y 65535.":                                                                             "The port must be between 1024 and 65535.",
	"Introduce el ID de organización, no su nombre ni la URL.":                                                             "Enter the organization ID, not its name or URL.",
	"La API key no puede contener espacios ni saltos de línea.":                                                            "The API key cannot contain spaces or line breaks.",
	"Detén el proxy o cancela el login antes de cambiar la conexión.":                                                      "Stop the proxy or cancel sign-in before changing the connection.",
	"Introduce tu API key personal de Kilo.":                                                                               "Enter your personal Kilo API key.",
	"Esta clave supera el tamaño portable del almacén de credenciales. Desmarca Recordar para usarla durante esta sesión.": "This key exceeds the credential store's portable size limit. Uncheck Remember to use it for this session.",
	"No se pudo guardar en el almacén del sistema. Desmarca Recordar para usar la clave solo durante esta sesión.":         "Could not save to the system store. Uncheck Remember to use the key only for this session.",
	"No se pudo borrar la clave guardada. Desbloquea el almacén del sistema y vuelve a intentarlo.":                        "Could not delete the saved key. Unlock your system credential store and try again.",
	"No se pudo guardar la configuración local. Revisa los permisos de la carpeta.":                                        "Could not save local settings. Check the folder permissions.",
	"Detén el proxy o cancela el login antes de olvidar la clave.":                                                         "Stop the proxy or cancel sign-in before forgetting the key.",
	"No se pudo borrar la clave del almacén del sistema.":                                                                  "Could not delete the key from the system credential store.",
	"No se pudo guardar la configuración local.":                                                                           "Could not save local settings.",
	"Kilo no responde. Comprueba tu conexión.":                                                                             "Kilo is not responding. Check your connection.",
	"Kilo devolvió HTTP {status}. Revisa la clave y la organización.":                                                      "Kilo returned HTTP {status}. Check your key and organization.",
	"Gateway accesible. El catálogo no verifica el saldo ni los permisos de generación de tu organización.":                "Gateway reachable. The catalog does not verify your organization's credits or generation permissions.",
	"Kilo devolvió una respuesta de login no válida.":                                                                      "Kilo returned an invalid sign-in response.",
	"La aplicación se está cerrando.":                                                                                      "The application is closing.",
	"Detén el proxy o cancela el login actual antes de conectar otra cuenta.":                                              "Stop the proxy or cancel the current sign-in before connecting another account.",
	"No se pudo iniciar el login de Kilo. Puedes usar tu API key manualmente.":                                             "Could not start Kilo sign-in. You can enter your API key manually.",
	"Hay demasiados logins pendientes en Kilo. Espera un momento y vuelve a intentarlo.":                                   "Too many Kilo sign-ins are pending. Wait a moment and try again.",
	"El código ha caducado. Vuelve a conectar con Kilo.":                                                                   "The code has expired. Connect with Kilo again.",
	"Se perdió la conexión con Kilo. Vuelve a iniciar el login.":                                                           "Connection to Kilo was lost. Start sign-in again.",
	"No se ha autorizado el acceso en Kilo.":                                                                               "Access was not authorized in Kilo.",
	"Kilo no ha devuelto una credencial válida.":                                                                           "Kilo did not return a valid credential.",
	"Sesión conectada. Selecciona tu equipo y pulsa Guardar y arrancar.":                                                   "Signed in. Select your team and click Save and start.",
	"Sesión conectada. Hemos seleccionado tu único equipo; pulsa Guardar y arrancar.":                                      "Signed in. Your only team has been selected; click Save and start.",
	"Sesión conectada, pero no se pudieron cargar tus equipos. Pulsa Cargar mis equipos o introduce el ID manualmente.":    "Signed in, but your teams could not be loaded. Click Load my teams or enter the ID manually.",
	"Sesión conectada. Kilo no devuelve ninguna organización para esta cuenta.":                                            "Signed in. Kilo returned no organizations for this account.",
	"Detén el proxy antes de cambiar de equipo.":                                                                           "Stop the proxy before changing teams.",
	"Conecta con Kilo o guarda una API key primero.":                                                                       "Connect with Kilo or save an API key first.",
	"No se pudieron cargar tus equipos. Revisa la conexión o vuelve a iniciar sesión.":                                     "Could not load your teams. Check your connection or sign in again.",
	"La conexión ha cambiado. Vuelve a cargar los equipos.":                                                                "The connection has changed. Load your teams again.",
	"No se pudo leer el almacén de credenciales. Introduce tu API key de nuevo.":                                           "Could not read the credential store. Enter your API key again.",
	"Idioma no compatible.": "Unsupported language.",
	"No se pudo guardar el idioma. Revisa los permisos de la carpeta.":     "Could not save the language. Check the folder permissions.",
	"No se pudo conectar con el login de Kilo.":                            "Could not connect to Kilo sign-in.",
	"organización no válida":                                               "invalid organization",
	"No se pudo conectar con Kilo. Comprueba la red e inténtalo de nuevo.": "Could not connect to Kilo. Check your network and try again.",
	"Este endpoint solo admite clientes locales de API.":                   "This endpoint only accepts local API clients.",
	"API key local incorrecta. Cópiala desde Kilo Proxy.":                  "Incorrect local API key. Copy it from Kilo Proxy.",
	"Endpoint no compatible. Usa la base URL terminada en /v1.":            "Unsupported endpoint. Use the base URL ending in /v1.",
	"La petición supera 32 MiB.":                                           "The request exceeds 32 MiB.",

	"Elige off, compress o upload para las imágenes grandes.":                                            "Choose off, compress, or upload for large images.",
	"No se pudo guardar la preferencia de imágenes. Revisa los permisos de la carpeta de configuración.": "Could not save the image preference. Check the configuration folder permissions.",
	"Kilo no pudo confirmar el borrado de las imágenes temporales. Los archivos pueden permanecer en tu cuenta de Kilo hasta que se ejecute su limpieza de subidas pendientes. Cerrar la app no garantiza su borrado.": "Kilo could not confirm deletion of temporary images. Files may remain in your Kilo account until its pending-upload cleanup runs. Closing the app does not guarantee deletion.",
}
