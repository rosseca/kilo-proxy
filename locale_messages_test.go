package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestNativeMessagesTranslateBackendWithoutChangingDetails(t *testing.T) {
	for _, test := range []struct{ source, language, want string }{
		{"Introduce tu API key personal de Kilo.", "en", "Enter your personal Kilo API key."},
		{"El código ha caducado. Vuelve a conectar con Kilo.", "en", "The code has expired. Connect with Kilo again."},
		{"Sesión conectada. Hemos seleccionado tu único equipo; pulsa Guardar y arrancar.", "en", "Signed in. Your only team has been selected; click Save and start."},
		{"No se pudo leer el almacén de credenciales. Introduce tu API key de nuevo.", "en", "Could not read the credential store. Enter your API key again."},
		{"Kilo devolvió HTTP 429. Revisa la clave y la organización.", "en", "Kilo returned HTTP 429. Check your key and organization."},
		{"Kilo returned HTTP 502. Check your key and organization.", "es", "Kilo devolvió HTTP 502. Revisa la clave y la organización."},
		{"No se pudo adaptar el esquema de herramientas para Anthropic: tools.13.input_schema oneOf", "en", "Could not adapt the tool schema for Anthropic: tools.13.input_schema oneOf"},
		{"API key local incorrecta. Cópiala desde Kilo Proxy.", "unknown", "Incorrect local API key. Copy it from Kilo Proxy."},
		{"El puerto debe estar entre 1024 y 65535.", "es", "El puerto debe estar entre 1024 y 65535."},
		{"Enter your personal Kilo API key.", "es", "Introduce tu API key personal de Kilo."},
	} {
		if got := nativeMessage(test.source, test.language); got != test.want {
			t.Fatalf("nativeMessage(%q,%q)=%q, want%q", test.source, test.language, got, test.want)
		}
	}
	for _, value := range []string{"private-token-123", "vendor/model", `{"message":"No se pudo completar la operación."}`, "upstream: Kilo devolvió HTTP 500. Revisa la clave y la organización.", "https://example.com/?Idioma=es", "Custom upstream error\n  source: tool-13"} {
		for _, language := range []string{"en", "es"} {
			if got := nativeMessage(value, language); got != value {
				t.Fatalf("translated data/unknown details %q", value)
			}
		}
	}
}

func TestNativeBackendMessagesMatchCanonicalBrowserTranslations(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required to compare the canonical browser translations")
	}
	keys := []string{}
	for source, translation := range nativeBackendMessages {
		if strings.TrimSpace(source) == "" || strings.TrimSpace(translation) == "" || source == translation {
			t.Fatalf("missing backend translation %q", source)
		}
		keys = append(keys, source)
	}
	data, _ := json.Marshal(keys)
	cmd := exec.Command(node, "--input-type=module", "-e", `import {translations} from './ui/i18n.mjs';let raw='';for await(const p of process.stdin)raw+=p;process.stdout.write(JSON.stringify(Object.fromEntries(JSON.parse(raw).filter(k=>Object.hasOwn(translations,k)).map(k=>[k,translations[k]]))));`)
	cmd.Stdin = bytes.NewReader(data)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("canonical translations unavailable: %v %s", err, output)
	}
	var canonical map[string]string
	if err = json.Unmarshal(output, &canonical); err != nil {
		t.Fatal(err)
	}
	if len(canonical) < 35 {
		t.Fatal("backend translation coverage unexpectedly dropped")
	}
	for source, want := range canonical {
		if got := nativeMessage(source, "en"); got != want {
			t.Fatalf("translation differs for%q: %q versus%q", source, got, want)
		}
	}
}
