# Local compatibility patches

Based on github.com/gogpu/systray v0.3.0 (MIT); Go sources and original license retained.

- Expose `InitError()` and clean up after a failed Create, allowing the app to keep its web panel available when native initialization fails.
- Disable Cocoa NSMenu automatic item validation and apply the initial disabled state. The application controls whether Start/Stop actions are enabled; AppKit otherwise re-enables every action whose selector exists.

Review these patches when updating the dependency. Native implementations still come from the upstream library.
