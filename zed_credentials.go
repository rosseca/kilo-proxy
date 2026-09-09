package main

import (
	"context"
	"encoding/hex"
	"errors"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"al.essio.dev/pkg/shellescape"
)

const zedCredentialTimeout = 30 * time.Second

var zedCredentialPath = regexp.MustCompile(`^/zed/[0-9a-f]{16}/v1$`)

var errZedCredentialStore = errors.New("Could not save Zed's local provider key in the system credential store. Unlock the credential store and try again, or paste the local key in Zed's provider settings.")

// This adapter writes only the local proxy credential in Zed's own format. It
// never reads credentials, enumerates a vault, or stores the Kilo account key.
// Reference: Zed v1.18.1 gpui_{macos,windows,linux}/src/*/platform.rs.
type zedCredential struct {
	APIURL, Key, Executable string
}

func validateZedCredential(c zedCredential) error {
	u, err := url.Parse(c.APIURL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || (u.Path != "/v1" && !zedCredentialPath.MatchString(u.Path)) || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return errors.New("Zed credentials require the local proxy API URL.")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1024 || port > 65535 || c.APIURL != "http://127.0.0.1:"+strconv.Itoa(port)+u.Path {
		return errors.New("Zed credentials require the local proxy API URL.")
	}
	if c.Key == "" || len(c.Key) > 1024 || !utf8.ValidString(c.Key) || strings.ContainsAny(c.Key, "\x00\r\n") {
		return errors.New("The local provider key is invalid.")
	}
	if c.Executable != "" && (!filepath.IsAbs(c.Executable) || len(c.Executable) > 2048 || strings.ContainsAny(c.Executable, "\x00\r\n")) {
		return errors.New("The Zed application path is invalid.")
	}
	return nil
}

func storeZedCredential(ctx context.Context, apiURL, key, executable string) error {
	return storeZedCredentialWith(ctx, zedCredential{apiURL, key, executable}, writeZedCredential)
}

func storeZedCredentialWith(ctx context.Context, credential zedCredential, write func(context.Context, zedCredential) error) error {
	if err := validateZedCredential(credential); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, zedCredentialTimeout)
	defer cancel()
	if ctx.Err() != nil {
		return errZedCredentialStore
	}
	if err := write(ctx, credential); err != nil {
		// Platform tools may include arguments or request payloads in errors.
		return errZedCredentialStore
	}
	return nil
}

// security's interactive input is not a shell. Hex password input avoids
// interpreting secret punctuation while storing exactly the original bytes.
// The process argument list contains only -i, never a password or command.
func zedMacCredentialInput(c zedCredential) (string, error) {
	if err := validateZedCredential(c); err != nil {
		return "", err
	}
	trusted := "-T ''" // Remove security's implicit broad creator access.
	if c.Executable != "" {
		trusted = "-T " + shellescape.Quote(c.Executable)
	}
	input := "add-internet-password -U -s " + shellescape.Quote(c.APIURL) + " -a Bearer " + trusted + " -X " + hex.EncodeToString([]byte(c.Key)) + "\n"
	if len(input) > 4096 {
		return "", errors.New("The Zed credential command exceeds the system limit.")
	}
	return input, nil
}
