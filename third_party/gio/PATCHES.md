# Gio runtime compatibility patches

Upstream: [gioui.org v0.10.2](https://github.com/gioui/gio/tree/v0.10.2), retained under its Unlicense OR MIT license in `LICENSE`.

The runtime source is copied from the verified Go module. Upstream unit tests, test fixtures, image references and repository metadata are omitted. `go.mod` replaces `gioui.org` with this directory. `kilo-local.patch` records every runtime modification and our focused tests relative to that exact version.

## macOS display timing

CoreVideo can report no active display on a virtual or remote desktop even while AppKit provides a working window server and Metal renderer. Upstream ignores CoreVideo return codes, and releasing its uninitialized view can call Go with an invalid zero handle. The patch checks creation and callback registration results and guards destruction of an uninitialized handle. When CoreVideo timing is unavailable on macOS, an idle-aware 60 Hz Go timer supplies frame scheduling. The native window and Metal rendering path remain unchanged. The timer stops while idle and exits on window destruction. iOS keeps the original failure behavior.

`go test ./app -run TestFallbackTimerStartsStopsAndCloses` checks start, stop, restart, display changes and teardown. The desktop self-test still requires rendered frames, backend interaction, a real clipboard and native close/reopen behavior; a missing window server or renderer remains a failure.

## Windows virtual GPU support

Gio first tries its normal Direct3D 11 hardware device. If creation fails, the native window renderer and screenshot renderer try Microsoft's built-in WARP software device. No DLL is downloaded or bundled. If both attempts fail, the error contains both causes. Successful hardware initialization is unchanged. Windows x64 and ARM64 CI runs the actual window and screenshot renderers, and packaging rejects linked DLLs outside the permitted Windows system libraries.

## Linux X11 empty clipboard

An X11 clipboard read receives `SelectionNotify` with property `None` when no selection owner exists or the requested text conversion is unavailable. Upstream drops that reply, leaving readers waiting indefinitely on a fresh desktop such as Xvfb. The patch delivers an empty text transfer for that reply, while retaining selection/property filtering and normal UTF-8 reads.

`go test ./app -run TestX11Clipboard` checks empty/unowned clipboards, normal Unicode text and unrelated or unreadable replies. Linux CI also retains the actual-window smoke test: preserve the initial clipboard, copy through the native UI, read the expected text back, and restore the prior text.

## Updating

Download and verify the new upstream module, reapply only still-needed changes, regenerate `kilo-local.patch`, and run all platform CI gates. Do not silently widen the patch or bypass graphical smoke checks to make an upgrade pass.
