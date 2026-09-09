package main

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"time"
)

func writeZedCredential(ctx context.Context, credential zedCredential) error {
	input, err := zedMacCredentialInput(credential)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/security", "-i")
	cmd.Stdin = strings.NewReader(input)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	cmd.WaitDelay = time.Second
	return cmd.Run()
}
