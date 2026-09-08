//go:build windows && (amd64 || arm64)

package systray

import (
	"testing"
	"unsafe"
)

// These offsets follow the Windows SDK's NOTIFYICONDATAW definition, including
// the uTimeout/uVersion union. Both supported Windows targets use the 64-bit ABI.
// https://learn.microsoft.com/en-us/windows/win32/api/shellapi/ns-shellapi-notifyicondataw
func TestNotifyIconDataWindowsABI(t *testing.T) {
	var value notifyIconData
	if size := unsafe.Sizeof(value); size != 976 {
		t.Fatalf("NOTIFYICONDATAW size = %d, Windows expects 976", size)
	}
	for _, field := range []struct {
		name      string
		got, want uintptr
	}{
		{"Wnd", unsafe.Offsetof(value.Wnd), 8},
		{"Icon", unsafe.Offsetof(value.Icon), 32},
		{"Tip", unsafe.Offsetof(value.Tip), 40},
		{"Info", unsafe.Offsetof(value.Info), 304},
		{"TimeoutOrVersion", unsafe.Offsetof(value.TimeoutOrVersion), 816},
		{"InfoTitle", unsafe.Offsetof(value.InfoTitle), 820},
		{"InfoFlags", unsafe.Offsetof(value.InfoFlags), 948},
		{"GuidItem", unsafe.Offsetof(value.GuidItem), 952},
		{"BalloonIcon", unsafe.Offsetof(value.BalloonIcon), 968},
	} {
		if field.got != field.want {
			t.Errorf("%s offset = %d, Windows expects %d", field.name, field.got, field.want)
		}
	}
}

func TestNotifyIconDataInitialRegistration(t *testing.T) {
	value := initialNotifyIconData(11, 22, 0x401)
	// NIM_ADD's Win32 contract requires the receiving window, identifier and a
	// valid icon. An icon field without NIF_ICON is ignored by the shell.
	if value.Wnd != 11 || value.Icon != 22 || value.ID == 0 {
		t.Fatal("initial registration omitted its window, identifier or icon")
	}
	if value.Flags&0x3 != 0x3 || value.CallbackMessage != 0x401 {
		t.Fatal("initial registration does not enable its icon and callback")
	}
	if value.Size != 976 {
		t.Fatalf("initial registration uses invalid Win32 structure size %d", value.Size)
	}
}
