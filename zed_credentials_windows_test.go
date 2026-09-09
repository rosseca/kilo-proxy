package main

import (
	"testing"

	"github.com/danieljoos/wincred"
)

func TestZedWindowsCredentialFormat(t *testing.T) {
	input := syntheticZedCredential()
	credential := zedWindowsCredential(input)
	if credential.TargetName != "zed:url="+input.APIURL || credential.UserName != "Bearer" || string(credential.CredentialBlob) != input.Key || credential.Persist != wincred.PersistLocalMachine {
		t.Fatal("credential does not match Zed's Windows format")
	}
	if len(credential.Attributes) != 0 || credential.TargetAlias != "" {
		t.Fatal("unrelated credential attributes")
	}
}
