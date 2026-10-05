//go:build !windows

package main

func queryWindowsAppPackagePaths(string) []string      { return nil }
func resolveWindowsCodexDesktop(string, string) string { return "" }
func resolveWindowsCodexCLI(string, string) string     { return "" }
