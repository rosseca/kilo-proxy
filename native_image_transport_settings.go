//go:build desktop

package main

import (
	"encoding/json"
	"image"

	"gioui.org/font"
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

type uxsImageMethod struct{ value, labelEN, labelES, captionEN, captionES, nameEN, nameES, explainEN, explainES string }

var uxsImageMethods = []uxsImageMethod{
	{"off", "Off", "Desactivado", "sends images as they are", "envía las imágenes tal cual", "Off", "Desactivado", "Images go out unchanged; very large requests can exceed the model's limit.", "Las imágenes salen tal cual; las peticiones muy grandes pueden superar el límite del modelo."},
	{"compress", "Compress here", "Comprimir aquí", "lighter copies, without leaving your computer", "copias más ligeras, sin salir de tu equipo", "Compress here", "Comprimir aquí", "Makes lighter copies on this computer; nothing is uploaded.", "Hace copias más ligeras en tu equipo; no se sube nada."},
	{"cloudflare", "Cloudflare", "Cloudflare", "temporary public link · recommended", "enlace público temporal · recomendado", "Cloudflare · temporary tunnel", "Cloudflare · túnel temporal", "Serves the original image from your computer with a link that is removed when done.", "Sirve la imagen original desde tu ordenador con un enlace que se borra al terminar."},
	{"tailscale", "Tailscale Funnel", "Tailscale Funnel", "if you already use Tailscale", "si ya usas Tailscale", "Tailscale Funnel", "Tailscale Funnel", "Serves the original image from your computer through Funnel; links are removed when done.", "Sirve la imagen original desde tu ordenador con Funnel; los enlaces se retiran al terminar."},
	{"litterbox", "Litterbox · experimental", "Litterbox · experimental", "temporary third-party hosting", "alojamiento temporal de terceros", "Litterbox · experimental", "Litterbox · experimental", "Uploads the image to a third-party host; the link expires on its own.", "Sube la imagen a un servicio externo; el enlace caduca solo."},
	{"upload", "Kilo · experimental", "Kilo · experimental", "Kilo attachment storage", "almacenamiento de adjuntos de Kilo", "Kilo · experimental", "Kilo · experimental", "Uploads the image to Kilo attachment storage and sends a temporary link.", "Sube la imagen al almacenamiento de adjuntos de Kilo y envía un enlace temporal."},
}

// imageTransportPanel is the Large images block: the chosen method in one row;
// Change opens the six methods and the chosen method's details.
func (u *nativeUI) imageTransportPanel() layout.Widget {
	saving := u.busy["PUT/api/image-transport-settings"]
	u.owner.mu.Lock()
	settings := normalizeImageTransportSettings(u.owner.config.ImageTransport)
	u.owner.mu.Unlock()
	current := uxsImageMethods[0]
	modeChoices := []nativeChoice{}
	for _, method := range uxsImageMethods {
		if method.value == settings.Mode {
			current = method
		}
		modeChoices = append(modeChoices, nativeChoice{Value: method.value, Label: u.tr(method.labelEN, method.labelES), Caption: u.tr(method.captionEN, method.captionES)})
	}
	dependency := u.imageDependency()
	name := u.uxsPlain(u.tr(current.nameEN, current.nameES))
	if settings.Mode == "cloudflare" && (dependency.Installed || dependency.Required) {
		found := dependency.Installed
		name = func(gtx layout.Context) layout.Dimensions {
			tone, text, icon := nativeSuccessFG, u.tr("cloudflared found", "cloudflared encontrado"), nativeIconCheck
			if !found {
				tone, text, icon = nativeWarningFG, u.tr("cloudflared missing", "falta cloudflared"), nativeIconWarning
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(u.uxsPlain(u.tr(current.nameEN, current.nameES))),
				layout.Rigid(layout.Spacer{Width: 14}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min = image.Pt(gtx.Dp(14), gtx.Dp(14))
					return icon.Layout(gtx, tone)
				}),
				layout.Rigid(layout.Spacer{Width: 4}.Layout),
				layout.Flexed(1, u.textStyle(13, text, tone, font.SemiBold)),
			)
		}
	}
	value := u.uxsValue(name, u.tr(current.explainEN, current.explainES))
	open := u.expanded["images.change"]
	children := []layout.Widget{}
	// A missing executable is always explained, even with the list closed.
	if settings.Mode == "cloudflare" && dependency.Required && !dependency.Installed {
		children = append(children, u.imageDependencyNotice(true))
	}
	if warning := nativeString(u.state, "imageUploadWarning"); warning != "" {
		children = append(children, u.message(nativeToneWarning, u.tr("Image cleanup needs attention", "Revisa la limpieza de imágenes")+": "+nativeMessage(warning, u.language)))
	}
	if open {
		children = append(children, u.uxsRadioList("images.mode.", modeChoices, settings.Mode, !saving, func(mode string) { next := settings; next.Mode = mode; u.saveImageTransportSettings(next) }))
		children = append(children, u.imageTransportDetails(settings, saving)...)
		children = append(children, u.pills(
			u.note(u.tr("Only the chosen method is used; if it fails, no other is tried.", "Solo se usa el método elegido; si falla, no se prueba otro.")),
			u.learnMore("images.learn-more", "https://github.com/rosseca/kilo-proxy/blob/main/docs/security-and-debugging.md#large-image-handling"),
		))
		if saving {
			children = append(children, u.note(u.tr("Saving…", "Guardando…")))
		}
	}
	var below layout.Widget
	if len(children) > 0 {
		below = u.column(children...)
	}
	row := u.uxsRow(u.tr("Method", "Método"), "", value, u.uxsToggleLink("images.change", "Change", "Cambiar"), below)
	return u.uxsBlock(u.tr("Large images", "Imágenes grandes"), u.tr("when a screenshot is bigger than the model accepts", "cuando una captura pesa más de lo que acepta el modelo"), row)
}

// imageTransportDetails explains the chosen method and holds its options.
func (u *nativeUI) imageTransportDetails(settings imageTransportSettings, saving bool) []layout.Widget {
	children := []layout.Widget{}
	switch settings.Mode {
	case "compress":
		profileChoices := []nativeChoice{{Value: "high", Label: u.tr("High quality", "Alta calidad")}, {Value: "balanced", Label: u.tr("Balanced", "Equilibrado")}, {Value: "small", Label: u.tr("Small size", "Tamaño pequeño")}}
		children = append(children, u.subheading(u.tr("Compression profile", "Perfil de compresión")), u.segmented("images.profile.", profileChoices, settings.Profile, !saving, func(profile string) { next := settings; next.Profile = profile; u.saveImageTransportSettings(next) }), u.note(u.tr("High quality: up to 3072 px / quality 92. Balanced: 2048 px / quality 85. Small size: 1280 px / quality 75. Aspect ratio is preserved.", "Alta calidad: hasta 3072 px / calidad 92. Equilibrado: 2048 px / calidad 85. Tamaño pequeño: 1280 px / calidad 75. Se conserva la proporción.")), u.note(u.tr("Tries lossless optimization first, then your selected profile if needed. Only outbound copies change. If the request still does not fit, it stops with an explanation; it never lowers quality further or uploads images automatically.", "Primero intenta optimizar sin pérdidas y después aplica el perfil elegido si hace falta. Solo cambian las copias enviadas. Si la petición sigue sin caber, se detiene con una explicación; nunca reduce más la calidad ni sube las imágenes automáticamente.")))
	case "cloudflare":
		// The dependency status is in the Method row and, when missing, above the list.
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
	children = append(children, u.note(u.tr("Changes apply to new requests without restarting. Earlier uploads keep their scheduled cleanup.", "Los cambios se aplican a las peticiones nuevas sin reiniciar. Las subidas anteriores conservan su limpieza programada.")))
	return children
}
