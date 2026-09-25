//go:build desktop

package main

import (
	"encoding/json"

	"gioui.org/layout"
)

func (u *nativeUI) saveImageTransportSettings(value imageTransportSettings) {
	u.call("PUT", "/api/image-transport-settings", value, func(raw json.RawMessage) {
		var saved imageTransportSettings
		if json.Unmarshal(raw, &saved) == nil {
			u.state["imageTransport"] = saved
			dependency := u.imageDependency()
			if saved.Mode != "cloudflare" || !dependency.Required {
				u.imageDependencyDismissed = false
			}
			dependency.Required = saved.Mode == "cloudflare"
			u.state["imageTransportDependency"] = dependency
			u.refreshState()
		}
	})
}

func (u *nativeUI) imageTransportPanel() layout.Widget {
	saving := u.busy["PUT/api/image-transport-settings"]
	u.owner.mu.Lock()
	settings := normalizeImageTransportSettings(u.owner.config.ImageTransport)
	u.owner.mu.Unlock()
	modeChoices := []nativeChoice{
		{Value: "off", Label: u.tr("Off", "Desactivado"), Caption: u.tr("Send images unchanged", "Enviar imágenes sin cambios")},
		{Value: "compress", Label: u.tr("Compress locally", "Comprimir en local"), Caption: u.tr("Smaller outbound copies", "Copias de envío más pequeñas")},
		{Value: "cloudflare", Label: u.tr("Cloudflare", "Cloudflare"), Caption: u.tr("Local files · public tunnel", "Archivos locales · túnel público")},
		{Value: "tailscale", Label: u.tr("Tailscale Funnel", "Tailscale Funnel"), Caption: u.tr("Use your existing Tailscale", "Usa tu instalación de Tailscale")},
		{Value: "litterbox", Label: u.tr("Litterbox · Experimental", "Litterbox · Experimental"), Caption: u.tr("Temporary third-party hosting", "Alojamiento temporal externo")},
		{Value: "upload", Label: u.tr("Kilo · Experimental", "Kilo · Experimental"), Caption: u.tr("Kilo attachment storage", "Almacenamiento de adjuntos de Kilo")},
	}
	children := []layout.Widget{
		u.optionCards("images.mode.", modeChoices, settings.Mode, !saving, func(mode string) { next := settings; next.Mode = mode; u.saveImageTransportSettings(next) }),
	}
	switch settings.Mode {
	case "compress":
		profileChoices := []nativeChoice{{Value: "high", Label: u.tr("High quality", "Alta calidad")}, {Value: "balanced", Label: u.tr("Balanced", "Equilibrado")}, {Value: "small", Label: u.tr("Small size", "Tamaño pequeño")}}
		children = append(children, u.subheading(u.tr("Compression profile", "Perfil de compresión")), u.segmented("images.profile.", profileChoices, settings.Profile, !saving, func(profile string) { next := settings; next.Profile = profile; u.saveImageTransportSettings(next) }), u.note(u.tr("High quality: up to 3072 px / quality 92. Balanced: 2048 px / quality 85. Small size: 1280 px / quality 75. Aspect ratio is preserved.", "Alta calidad: hasta 3072 px / calidad 92. Equilibrado: 2048 px / calidad 85. Tamaño pequeño: 1280 px / calidad 75. Se conserva la proporción.")), u.note(u.tr("Tries lossless optimization first, then your selected profile if needed. Only outbound copies change. If the request still does not fit, it stops with an explanation; it never lowers quality further or uploads images automatically.", "Primero intenta optimizar sin pérdidas y después aplica el perfil elegido si hace falta. Solo cambian las copias enviadas. Si la petición sigue sin caber, se detiene con una explicación; nunca reduce más la calidad ni sube las imágenes automáticamente.")))
	case "cloudflare":
		if dependency := u.imageDependency(); dependency.Installed {
			children = append(children, u.message(nativeToneSuccess, u.tr("cloudflared found", "cloudflared encontrado")))
		} else if dependency.Required {
			children = append(children, u.imageDependencyNotice(true))
		}
		children = append(children,
			u.subheading(u.tr("Cloudflare quick tunnel", "Túnel rápido de Cloudflare")),
			u.note(u.tr("Requires cloudflared installed on this computer and available on PATH. No Cloudflare account or S3 bucket is needed. The tunnel starts when a large request needs it.", "Requiere cloudflared instalado en este equipo y disponible en PATH. No necesita cuenta de Cloudflare ni un bucket S3. El túnel se inicia cuando lo necesita una petición grande.")),
			u.note(u.tr("Serves original image bytes from this computer through public, unguessable links. Links are removed after the request finishes or is cancelled. Keep Kilo Proxy running while images are in use.", "Sirve las imágenes originales desde este equipo mediante enlaces públicos difíciles de adivinar. Los enlaces se retiran al terminar o cancelar la petición. Mantén Kilo Proxy abierto mientras se usan las imágenes.")),
		)
	case "tailscale":
		children = append(children,
			u.subheading(u.tr("Tailscale Funnel", "Tailscale Funnel")),
			u.note(u.tr("Requires the tailscale command, a signed-in account, and Funnel enabled for this device. Uses a dedicated HTTPS port 8443; leave it free for Kilo Proxy.", "Requiere el comando tailscale, una cuenta con sesión iniciada y Funnel habilitado para este dispositivo. Usa el puerto HTTPS 8443; déjalo libre para Kilo Proxy.")),
			u.note(u.tr("Funnel makes image links publicly reachable, including outside your tailnet. Images stay on this computer and links are removed after the request finishes or is cancelled.", "Funnel permite acceder a los enlaces de imágenes desde Internet, incluso fuera de tu tailnet. Las imágenes permanecen en este equipo y los enlaces se retiran al terminar o cancelar la petición.")),
		)
	case "litterbox":
		children = append(children,
			u.subheading(u.tr("Temporary public hosting", "Alojamiento público temporal")),
			u.message(nativeToneWarning, u.tr("Experimental: availability could not be confirmed. If uploads fail, choose Cloudflare or local compression.", "Experimental: no se ha podido confirmar la disponibilidad. Si fallan las subidas, elige Cloudflare o la compresión local.")),
			u.note(u.tr("Uploads original images to Litterbox, a third-party service. Anyone with the link can access them until expiry. No account or extra executable is required. Choose this only for images you can share with that service.", "Sube las imágenes originales a Litterbox, un servicio externo. Cualquiera con el enlace puede acceder hasta que caduque. No requiere cuenta ni ejecutables adicionales. Elígelo solo para imágenes que puedas compartir con ese servicio.")),
			u.note(u.tr("Litterbox requires prior approval for commercial service use; see its FAQ before using it for your team.", "Litterbox requiere autorización previa para uso en servicios comerciales; consulta sus preguntas frecuentes antes de usarlo con tu equipo.")),
			u.button("images.litterbox.faq", u.tr("Litterbox FAQ", "Preguntas frecuentes de Litterbox"), func() { u.open("https://litterbox.catbox.moe/faq.php") }),
			u.label(u.tr("Link expiry", "Caducidad del enlace")),
		)
		expiries := []nativeChoice{{Value: "1h", Label: u.tr("1 hour", "1 hora")}, {Value: "12h", Label: u.tr("12 hours", "12 horas")}, {Value: "24h", Label: u.tr("24 hours", "24 horas")}, {Value: "72h", Label: u.tr("72 hours", "72 horas")}}
		children = append(children, u.segmented("images.litterboxTTL.", expiries, settings.LitterboxTTL, !saving, func(ttl string) { next := settings; next.LitterboxTTL = ttl; u.saveImageTransportSettings(next) }), u.note(u.tr("Litterbox handles expiry. Kilo Proxy cannot delete these uploads early, even after you switch modes or close the app.", "Litterbox gestiona la caducidad. Kilo Proxy no puede borrar estas subidas antes, aunque cambies de modo o cierres la app.")))
	case "upload":
		children = append(children, u.subheading(u.tr("Kilo attachment storage", "Almacenamiento de adjuntos de Kilo")))
		children = append(children,
			u.note(u.tr("Uploads original image bytes to Kilo and sends temporary links. No resizing, tunnel, or storage setup is needed. Up to 5 unique images uploaded per request, 20 MiB each.", "Sube las imágenes originales a Kilo y envía enlaces temporales. No cambia su tamaño ni requiere configurar un túnel o almacenamiento. Hasta 5 imágenes únicas subidas por petición y 20 MiB por imagen.")),
			u.message(nativeToneWarning, u.tr("Experimental: uses Kilo's Cloud Agent attachment storage with your account. This is not a documented Gateway integration and may stop working.", "Experimental: usa el almacenamiento de adjuntos de Cloud Agent de Kilo con tu cuenta. No es una integración documentada del Gateway y puede dejar de funcionar.")),
			u.note(u.tr("Deletion is requested after completion or cancellation. Network failures or an app crash can leave remote copies behind; an expired link does not mean the image was deleted. Any unconfirmed deletion is shown here.", "Se solicita el borrado al terminar o cancelar. Un fallo de red o el cierre inesperado de la app puede dejar copias remotas; que un enlace caduque no significa que la imagen se haya borrado. Los borrados sin confirmar se muestran aquí.")),
		)
	default:
		children = append(children, u.note(u.tr("Images pass through unchanged. Large requests can still exceed Kilo's limit and need conversation compaction or fewer attachments.", "Las imágenes se envían sin cambios. Las peticiones grandes pueden superar el límite de Kilo y requerir compactar la conversación o reducir los adjuntos.")))
	}
	children = append(children, u.learnMore("images.learn-more", "https://github.com/rosseca/kilo-proxy/blob/main/docs/security-and-debugging.md#large-image-handling"))
	children = append(children, u.note(u.tr("Cloudflare is the default for new profiles. Your saved choice is kept. Changes save automatically for new requests, without restarting. Only the selected method is used; failures never switch to another backend or upload service. Earlier uploads still receive their scheduled cleanup.", "Cloudflare es la opción predeterminada para perfiles nuevos. Se conserva tu elección guardada. Los cambios se guardan automáticamente para nuevas peticiones, sin reiniciar. Solo se usa el método elegido; los fallos nunca cambian a otro backend o servicio de subida. Las subidas anteriores conservan su limpieza programada.")))
	if warning := nativeString(u.state, "imageUploadWarning"); warning != "" {
		children = append(children, u.message(nativeToneWarning, u.tr("Image cleanup needs attention", "Revisa la limpieza de imágenes")+": "+nativeMessage(warning, u.language)))
	}
	if saving {
		children = append(children, u.note(u.tr("Saving image preference…", "Guardando preferencia de imágenes…")))
	}
	return nativeSettingsPanelWithGap(u.section(u.tr("Large images", "Imágenes grandes"), u.tr("Choose how oversized image requests are handled.", "Elige cómo se gestionan las peticiones con imágenes grandes."), children...))
}
