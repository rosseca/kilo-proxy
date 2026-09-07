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
