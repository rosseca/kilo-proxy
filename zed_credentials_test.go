package main

import (
	"context"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func syntheticZedCredential() zedCredential {
	return zedCredential{APIURL: "http://127.0.0.1:8877/zed/0123456789abcdef/v1", Key: "synthetic-key-'$()é", Executable: ""}
}

func TestZedCredentialScopeAndErrors(t *testing.T) {
	for _, url := range []string{"https://example.com/v1", "http://localhost:8877/v1", "http://127.0.0.1:0/v1", "http://127.0.0.1:8877/v1?secret=bad", "http://127.0.0.1:8877/v1#fragment", "http://127.0.0.1:8877/zed/not-a-valid-tag/v1", "http://127.0.0.1:8877/zed/0123456789ABCDEF/v1", "http://127.0.0.1:8877/zed/0123456789abcdef/v1/", "http://user@127.0.0.1:8877/v1", "http://127.0.0.1:8877/%76%31"} {
		c := syntheticZedCredential()
		c.APIURL = url
		called := false
		if err := storeZedCredentialWith(context.Background(), c, func(context.Context, zedCredential) error { called = true; return nil }); err == nil || called {
			t.Errorf("accepted non-owned URL %q", url)
		}
	}
	for _, key := range []string{"", "bad\nsecond-command", "bad\x00key", strings.Repeat("x", 1025)} {
		c := syntheticZedCredential()
		c.Key = key
		if err := storeZedCredentialWith(context.Background(), c, func(context.Context, zedCredential) error {
			t.Error("invalid key reached credential store")
			return nil
		}); err == nil {
			t.Error("invalid local key accepted")
		}
	}
	c := syntheticZedCredential()
	for _, url := range []string{c.APIURL, "http://127.0.0.1:8877/v1"} {
		c.APIURL = url
		called := false
		if err := storeZedCredentialWith(context.Background(), c, func(ctx context.Context, got zedCredential) error {
			called = true
			deadline, ok := ctx.Deadline()
			if got != c || !ok || time.Until(deadline) > zedCredentialTimeout {
				t.Error("write lost request or timeout")
			}
			return errors.New("platform error echoed " + c.Key)
		}); !errors.Is(err, errZedCredentialStore) || strings.Contains(err.Error(), c.Key) || !called {
			t.Fatal("platform error not sanitized")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := storeZedCredentialWith(ctx, c, func(context.Context, zedCredential) error { t.Error("write called after cancel"); return nil }); err == nil {
		t.Fatal("cancelled store succeeded")
	}
}

func TestZedMacCredentialCommandIsWriteOnlyAndRaw(t *testing.T) {
	c := syntheticZedCredential()
	input, err := zedMacCredentialInput(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"add-internet-password -U -s ", c.APIURL, " -a Bearer ", "-T ''", " -X " + hex.EncodeToString([]byte(c.Key)) + "\n"} {
		if !strings.Contains(input, token) {
			t.Errorf("missing command contract %q", token)
		}
	}
	for _, forbidden := range []string{c.Key, "generic-password", " -A", "find-", "dump-", "partition-list", "go-keyring"} {
		if strings.Contains(input, forbidden) {
			t.Errorf("unexpected command content %q", forbidden)
		}
	}
	if strings.Count(input, "\n") != 1 {
		t.Error("not exactly one write command")
	}
}

type fakeZedSecretService struct {
	t           *testing.T
	want        zedCredential
	methods     []string
	failCreate  bool
	promptError bool
}

func (s *fakeZedSecretService) call(ctx context.Context, path dbus.ObjectPath, method string, args ...any) *dbus.Call {
	s.methods = append(s.methods, method)
	if ctx.Err() != nil {
		return &dbus.Call{Err: ctx.Err()}
	}
	switch method {
	case zedSecretServiceInterface + ".OpenSession":
		if path != zedSecretServicePath || args[0] != "plain" {
			s.t.Error("incorrect session request")
		}
		return &dbus.Call{Body: []any{dbus.MakeVariant(""), dbus.ObjectPath("/session/one")}}
	case zedSecretServiceInterface + ".ReadAlias":
		if args[0] != "default" {
			s.t.Error("not default collection")
		}
		return &dbus.Call{Body: []any{dbus.ObjectPath("/collection/login")}}
	case zedSecretServiceInterface + ".Unlock":
		if !reflect.DeepEqual(args, []any{[]dbus.ObjectPath{"/collection/login"}}) {
			s.t.Error("unlock affected other collections")
		}
		return &dbus.Call{Body: []any{[]dbus.ObjectPath{"/collection/login"}, dbus.ObjectPath("/")}}
	case zedSecretCollectionInterface + ".CreateItem":
		if s.failCreate {
			return &dbus.Call{Err: errors.New("synthetic write failure")}
		}
		properties := args[0].(map[string]dbus.Variant)
		if len(properties) != 2 || properties[zedSecretItemInterface+".Label"].Value() != "zed-github-account" || !reflect.DeepEqual(properties[zedSecretItemInterface+".Attributes"].Value(), map[string]string{"url": s.want.APIURL, "username": "Bearer"}) {
			s.t.Error("does not match Zed label/attributes")
		}
		secret := args[1].(zedSecret)
		if path != "/collection/login" || secret.Session != "/session/one" || string(secret.Value) != s.want.Key || len(secret.Parameters) != 0 || args[2] != true {
			s.t.Error("secret is not a raw exact-item replacement")
		}
		return &dbus.Call{Body: []any{dbus.ObjectPath("/item/one"), dbus.ObjectPath("/")}}
	case "org.freedesktop.Secret.Session.Close":
		if path != "/session/one" {
			s.t.Error("closed wrong session")
		}
		return &dbus.Call{}
	default:
		s.t.Errorf("unexpected credential service operation %s", method)
		return &dbus.Call{Err: errors.New("unexpected call")}
	}
}
func (s *fakeZedSecretService) prompt(context.Context, dbus.ObjectPath) (dbus.Variant, error) {
	if s.promptError {
		return dbus.Variant{}, errors.New("prompt cancelled")
	}
	return dbus.MakeVariant(""), nil
}

func TestZedSecretServiceWriteContract(t *testing.T) {
	for _, outcome := range []string{"success", "write failure", "prompt cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			c := syntheticZedCredential()
			service := &fakeZedSecretService{t: t, want: c, failCreate: outcome == "write failure", promptError: outcome == "prompt cancelled"}
			err := writeZedSecretService(context.Background(), c, service)
			if (err == nil) != (outcome == "success") {
				t.Fatalf("wrong result: %v", err)
			}
			if service.methods[len(service.methods)-1] != "org.freedesktop.Secret.Session.Close" {
				t.Error("session was not closed")
			}
		})
	}
}
