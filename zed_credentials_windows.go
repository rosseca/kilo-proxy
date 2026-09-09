package main

import (
	"context"

	"github.com/danieljoos/wincred"
)

func zedWindowsCredential(c zedCredential) *wincred.GenericCredential {
	credential := wincred.NewGenericCredential("zed:url=" + c.APIURL)
	credential.UserName = "Bearer"
	credential.CredentialBlob = []byte(c.Key)
	credential.Persist = wincred.PersistLocalMachine
	return credential
}

func writeZedCredential(ctx context.Context, credential zedCredential) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// CredWrite is a local, synchronous OS operation with no UI or network. A
	// detached timeout would allow a credential write after reporting failure.
	return zedWindowsCredential(credential).Write()
}
