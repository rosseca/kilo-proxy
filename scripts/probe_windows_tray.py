#!/usr/bin/env python3
"""Diagnose native notification registration independently of Go and systray.

This CI probe creates only its own hidden windows and short-lived stock icons.
It never replaces the mandatory production-app smoke test or alters the shell.
"""
import ctypes
from ctypes import wintypes
import argparse
import json
from pathlib import Path
import platform
import uuid


def probe(report):
    if platform.system() != 'Windows':
        raise RuntimeError('The notification probe requires Windows')
    from smoke_desktop import windows_desktop_context

    class GUID(ctypes.Structure):
        _fields_ = [('Data1', wintypes.DWORD), ('Data2', wintypes.WORD),
                    ('Data3', wintypes.WORD), ('Data4', ctypes.c_ubyte * 8)]

    class NOTIFYICONDATAW(ctypes.Structure):
        _fields_ = [('Size', wintypes.DWORD), ('Wnd', wintypes.HWND),
                    ('ID', wintypes.UINT), ('Flags', wintypes.UINT),
                    ('Callback', wintypes.UINT), ('Icon', wintypes.HICON),
                    ('Tip', wintypes.WCHAR * 128), ('State', wintypes.DWORD),
                    ('StateMask', wintypes.DWORD), ('Info', wintypes.WCHAR * 256),
                    ('TimeoutOrVersion', wintypes.UINT),
                    ('InfoTitle', wintypes.WCHAR * 64), ('InfoFlags', wintypes.DWORD),
                    ('Guid', GUID), ('BalloonIcon', wintypes.HICON)]

    user32 = ctypes.WinDLL('user32', use_last_error=True)
    shell32 = ctypes.WinDLL('shell32', use_last_error=True)
    ole32 = ctypes.WinDLL('ole32', use_last_error=True)
    user32.CreateWindowExW.argtypes = [wintypes.DWORD, wintypes.LPCWSTR, wintypes.LPCWSTR,
        wintypes.DWORD, ctypes.c_int, ctypes.c_int, ctypes.c_int, ctypes.c_int,
        wintypes.HWND, wintypes.HMENU, wintypes.HINSTANCE, ctypes.c_void_p]
    user32.CreateWindowExW.restype = wintypes.HWND
    user32.DestroyWindow.argtypes = [wintypes.HWND]
    user32.DestroyWindow.restype = wintypes.BOOL
    user32.IsWindow.argtypes = [wintypes.HWND]
    user32.IsWindow.restype = wintypes.BOOL
    user32.LoadIconW.argtypes = [wintypes.HINSTANCE, ctypes.c_void_p]
    user32.LoadIconW.restype = wintypes.HICON
    shell32.Shell_NotifyIconW.argtypes = [wintypes.DWORD, ctypes.POINTER(NOTIFYICONDATAW)]
    shell32.Shell_NotifyIconW.restype = wintypes.BOOL
    ole32.CoInitializeEx.argtypes = [ctypes.c_void_p, wintypes.DWORD]
    ole32.CoInitializeEx.restype = ctypes.c_long
    ole32.CoUninitialize.argtypes = []
    ole32.CoUninitialize.restype = None
    stock_icon = user32.LoadIconW(None, ctypes.c_void_p(32512))
    result = {'desktop': windows_desktop_context(), 'stock_icon_valid': bool(stock_icon),
              'pointer_size': ctypes.sizeof(ctypes.c_void_p),
              'structure_size': ctypes.sizeof(NOTIFYICONDATAW),
              'offsets': {name: getattr(NOTIFYICONDATAW, name).offset
                          for name in ('Wnd', 'Icon', 'Info', 'TimeoutOrVersion',
                                       'InfoTitle', 'InfoFlags', 'Guid', 'BalloonIcon')},
              'registrations': []}
    if not stock_icon:
        raise ctypes.WinError(ctypes.get_last_error())

    def registration_cases(stage):
        for window_kind, parent in [('hidden-top-level', None), ('message-only', ctypes.c_void_p(-3))]:
            hwnd = user32.CreateWindowExW(0, 'STATIC', 'Kilo Proxy CI notification probe',
                                        0, 0, 0, 1, 1, parent, None, None, None)
            if not hwnd:
                raise ctypes.WinError(ctypes.get_last_error())
            try:
                for index, (name, flags, size) in enumerate([
                    ('callback-only', 1, ctypes.sizeof(NOTIFYICONDATAW)),
                    ('icon-and-callback', 3, ctypes.sizeof(NOTIFYICONDATAW)),
                    ('icon-callback-tip', 7, ctypes.sizeof(NOTIFYICONDATAW)),
                    ('icon-with-guid', 0x27, ctypes.sizeof(NOTIFYICONDATAW)),
                    ('legacy-v3', 7, NOTIFYICONDATAW.BalloonIcon.offset),
                    ('legacy-v2', 7, NOTIFYICONDATAW.Guid.offset),
                ], start=1):
                    value = NOTIFYICONDATAW(Size=size, Wnd=hwnd, ID=index,
                        Flags=flags, Callback=0x8001, Icon=stock_icon,
                        Tip='Kilo Proxy CI probe')
                    value.Guid = GUID.from_buffer_copy(uuid.uuid4().bytes_le)
                    ctypes.set_last_error(0)
                    added = bool(shell32.Shell_NotifyIconW(0, ctypes.byref(value)))
                    error = ctypes.get_last_error()
                    try:
                        result['registrations'].append({'stage': stage, 'window': window_kind,
                            'hwnd_valid': bool(user32.IsWindow(hwnd)), 'case': name,
                            'size': size, 'flags': flags, 'added': added, 'last_error': error})
                    finally:
                        # HWND + ID/GUID belong to this probe only. Delete even
                        # on reported failure in case the shell partially added it.
                        shell32.Shell_NotifyIconW(2, ctypes.byref(value))
            finally:
                user32.DestroyWindow(hwnd)

    registration_cases('no-com-initialization')
    com_result = ole32.CoInitializeEx(None, 2)
    result['com_sta_result'] = com_result
    try:
        registration_cases('after-com-sta')
    finally:
        if com_result in (0, 1):
            ole32.CoUninitialize()
    # LoadIconW returns a shared stock handle; DestroyIcon must not free it.
    encoded = json.dumps(result, indent=2)
    if report:
        report.parent.mkdir(parents=True, exist_ok=True)
        report.write_text(encoded, encoding='utf-8')
    print(encoded, flush=True)
    print('Diagnostic probe complete; the production native smoke must still pass.', flush=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--report', type=Path)
    probe(parser.parse_args().report)
