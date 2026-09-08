# Windows tray structure compatibility

Upstream: [fyne.io/systray commit af4e8e793ec4](https://github.com/fyne-io/systray/tree/af4e8e793ec4), pinned as `v1.12.3-0.20260810170012-af4e8e793ec4`, under the Apache 2.0 license in `LICENSE`. Runtime sources are retained from the verified Go module. Examples, repository metadata and upstream tests are omitted.

The structure correction represents Windows' `uTimeout` / `uVersion` union as one `uint32` in `notifyIconData`. Upstream declares two fields, producing a 984-byte structure instead of the 976-byte `NOTIFYICONDATAW` required by the Windows 64-bit ABI. This also shifts the title, flags, GUID and balloon-icon fields. The invalid size is passed directly to `Shell_NotifyIconW` and can cause it to reject tray initialization. Errors now identify that API call; the smoke runner also reports taskbar presence and its process session without modifying the desktop.

The first `NIM_ADD` now also supplies the already loaded, checked stock icon with `NIF_ICON`. Upstream supplies only its callback flag and a zero icon, while [Microsoft's initial registration contract](https://learn.microsoft.com/en-us/windows/win32/shell/taskbar#adding-and-deleting-taskbar-icons-in-the-notification-area) requires the window, identifier and icon handle. The app's normal ready callback replaces the stock icon with Kilo Proxy's artwork. Notification versions and mouse-event handling are unchanged.

The definition follows [Microsoft's Windows SDK documentation](https://learn.microsoft.com/en-us/windows/win32/api/shellapi/ns-shellapi-notifyicondataw). `notify_icon_windows_test.go` checks the structure size, field offsets and initial registration invariants on both Windows x64 and ARM64. Full native CI still requires successful tray setup, language changes, clipboard, proxy continuity, window close/reopen and shutdown.

`kilo-proxy.patch` records the exact source change and its ABI regression test. When updating the dependency, compare against the new upstream definition, retain only changes that remain necessary, regenerate this patch, and run the six native and package jobs.
