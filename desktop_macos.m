//go:build desktop && darwin

#import <AppKit/AppKit.h>
#import <objc/runtime.h>

extern void kiloNativeQuit(void);
extern void kiloNativeReopen(void);
extern void kiloNativeHide(void);

// Extend Gio's delegate rather than replacing it: its launch and URL handling
// remain intact. Defer OS termination until Go has stopped listeners and saved
// state; the desktop controller exits after cleanup.
static NSApplicationTerminateReply kiloShouldTerminate(id self, SEL selector, NSApplication *sender) {
    kiloNativeQuit();
    return NSTerminateCancel;
}

static BOOL kiloShouldReopen(id self, SEL selector, NSApplication *sender, BOOL visible) {
    kiloNativeReopen();
    return YES;
}

static BOOL kiloWindowShouldClose(id self, SEL selector, NSWindow *window) {
    [window orderOut:nil];
    kiloNativeHide();
    return NO;
}

int kiloWindowVisible(void *view) {
    return [(__bridge NSView *)view window].visible ? 1 : 0;
}

void kiloInstallLifecycle(void *view) {
    Class windowDelegateClass = [[(__bridge NSView *)view window].delegate class];
    class_addMethod(windowDelegateClass, @selector(windowShouldClose:), (IMP)kiloWindowShouldClose, protocol_getMethodDescription(@protocol(NSWindowDelegate), @selector(windowShouldClose:), NO, YES).types);
    Class delegateClass = [[NSApp delegate] class];
    class_addMethod(delegateClass, @selector(applicationShouldTerminate:), (IMP)kiloShouldTerminate, protocol_getMethodDescription(@protocol(NSApplicationDelegate), @selector(applicationShouldTerminate:), NO, YES).types);
    class_addMethod(delegateClass, @selector(applicationShouldHandleReopen:hasVisibleWindows:), (IMP)kiloShouldReopen, protocol_getMethodDescription(@protocol(NSApplicationDelegate), @selector(applicationShouldHandleReopen:hasVisibleWindows:), NO, YES).types);
}
