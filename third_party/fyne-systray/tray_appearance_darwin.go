//go:build !ios

package systray

/*
#include <stdbool.h>
#include <stdlib.h>
char* copyTrayAppearance(bool *hasIcon);
*/
import "C"

import "unsafe"

// TrayAppearance snapshots the real status button on Cocoa's main thread.
// The last result reports whether this inspection is supported on this OS.
func TrayAppearance() (title string, hasIcon, supported bool) {
	var icon C.bool
	value := C.copyTrayAppearance(&icon)
	defer C.free(unsafe.Pointer(value))
	return C.GoString(value), bool(icon), true
}
