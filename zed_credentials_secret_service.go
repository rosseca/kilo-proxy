package main

import (
	"context"
	"errors"

	"github.com/godbus/dbus/v5"
)

const (
	zedSecretServiceName         = "org.freedesktop.secrets"
	zedSecretServicePath         = dbus.ObjectPath("/org/freedesktop/secrets")
	zedSecretServiceInterface    = "org.freedesktop.Secret.Service"
	zedSecretCollectionInterface = "org.freedesktop.Secret.Collection"
	zedSecretItemInterface       = "org.freedesktop.Secret.Item"
	zedSecretPromptInterface     = "org.freedesktop.Secret.Prompt"
)

type zedSecret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// A deliberately write-only interface: no search, GetSecret, or enumeration.
type zedSecretService interface {
	call(context.Context, dbus.ObjectPath, string, ...any) *dbus.Call
	prompt(context.Context, dbus.ObjectPath) (dbus.Variant, error)
}

func writeZedSecretService(ctx context.Context, credential zedCredential, service zedSecretService) error {
	var output dbus.Variant
	var session dbus.ObjectPath
	if err := service.call(ctx, zedSecretServicePath, zedSecretServiceInterface+".OpenSession", "plain", dbus.MakeVariant("")).Store(&output, &session); err != nil {
		return err
	}
	if session == "/" || !session.IsValid() {
		return errors.New("No credential session.")
	}
	defer service.call(ctx, session, "org.freedesktop.Secret.Session.Close")
	var collection dbus.ObjectPath
	if err := service.call(ctx, zedSecretServicePath, zedSecretServiceInterface+".ReadAlias", "default").Store(&collection); err != nil {
		return err
	}
	if collection == "/" || !collection.IsValid() {
		return errors.New("No default credential collection.")
	}
	var unlocked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := service.call(ctx, zedSecretServicePath, zedSecretServiceInterface+".Unlock", []dbus.ObjectPath{collection}).Store(&unlocked, &prompt); err != nil {
		return err
	}
	if _, err := service.prompt(ctx, prompt); err != nil {
		return err
	}
	properties := map[string]dbus.Variant{
		zedSecretItemInterface + ".Label":      dbus.MakeVariant("zed-github-account"),
		zedSecretItemInterface + ".Attributes": dbus.MakeVariant(map[string]string{"url": credential.APIURL, "username": "Bearer"}),
	}
	secret := zedSecret{Session: session, Parameters: []byte{}, Value: []byte(credential.Key), ContentType: "text/plain; charset=utf8"}
	var item dbus.ObjectPath
	if err := service.call(ctx, collection, zedSecretCollectionInterface+".CreateItem", properties, secret, true).Store(&item, &prompt); err != nil {
		return err
	}
	result, err := service.prompt(ctx, prompt)
	if err != nil {
		return err
	}
	if prompt != "/" {
		item, _ = result.Value().(dbus.ObjectPath)
	}
	if item == "/" || !item.IsValid() {
		return errors.New("Credential item was not created.")
	}
	return nil
}
