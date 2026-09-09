//go:build !darwin && !windows && !linux

package main

import "context"

func writeZedCredential(context.Context, zedCredential) error { return errZedCredentialStore }
