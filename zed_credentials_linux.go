package main

import (
	"context"
	"errors"
	"time"

	"github.com/godbus/dbus/v5"
)

type zedSystemSecretService struct{ conn *dbus.Conn }

func (s zedSystemSecretService) call(ctx context.Context, path dbus.ObjectPath, method string, args ...any) *dbus.Call {
	return s.conn.Object(zedSecretServiceName, path).CallWithContext(ctx, method, 0, args...)
}

func (s zedSystemSecretService) prompt(ctx context.Context, path dbus.ObjectPath) (dbus.Variant, error) {
	if path == "/" {
		return dbus.MakeVariant(""), nil
	}
	if !path.IsValid() {
		return dbus.Variant{}, errors.New("Invalid credential prompt.")
	}
	options := []dbus.MatchOption{dbus.WithMatchSender(zedSecretServiceName), dbus.WithMatchObjectPath(path), dbus.WithMatchInterface(zedSecretPromptInterface), dbus.WithMatchMember("Completed")}
	signals := make(chan *dbus.Signal, 4)
	s.conn.Signal(signals)
	defer s.conn.RemoveSignal(signals)
	if err := s.conn.AddMatchSignalContext(ctx, options...); err != nil {
		return dbus.Variant{}, err
	}
	defer s.conn.RemoveMatchSignalContext(ctx, options...)
	if err := s.call(ctx, path, zedSecretPromptInterface+".Prompt", "").Err; err != nil {
		return dbus.Variant{}, err
	}
	for {
		select {
		case <-ctx.Done():
			// Closing our connection alone does not dismiss a service-owned UI.
			cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = s.call(cleanup, path, zedSecretPromptInterface+".Dismiss").Err
			cancel()
			return dbus.Variant{}, ctx.Err()
		case signal, ok := <-signals:
			if !ok {
				return dbus.Variant{}, errors.New("Credential service disconnected.")
			}
			if signal.Path != path || signal.Name != zedSecretPromptInterface+".Completed" {
				continue
			}
			if len(signal.Body) != 2 {
				return dbus.Variant{}, errors.New("Invalid credential prompt result.")
			}
			dismissed, valid := signal.Body[0].(bool)
			result, typed := signal.Body[1].(dbus.Variant)
			if !valid || !typed || dismissed {
				return dbus.Variant{}, errors.New("Credential prompt was not accepted.")
			}
			return result, nil
		}
	}
}

func writeZedCredential(ctx context.Context, credential zedCredential) error {
	// Use the existing desktop session bus; never start an auxiliary daemon.
	conn, err := dbus.SessionBusPrivateNoAutoStartup(dbus.WithContext(ctx))
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.Auth(nil); err != nil {
		return err
	}
	if err := conn.Hello(); err != nil {
		return err
	}
	return writeZedSecretService(ctx, credential, zedSystemSecretService{conn})
}
