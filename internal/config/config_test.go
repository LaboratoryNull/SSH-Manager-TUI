package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestStoreRoundTripAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	store := NewStoreAt(path)
	want := Config{UserProfiles: []UserProfile{{Name: "prod-root", User: "root", KeyPath: "~/.ssh/prod"}}, Sessions: []Session{{
		Name: "prod", Host: "server", Port: 22, User: "alice", Password: "plain",
		Group: "Production", ConnectionCount: 12,
		PortForwards: []PortForward{{
			Name: "postgres", Type: ForwardLocal, BindHost: "127.0.0.1", BindPort: 5433,
			TargetHost: "database", TargetPort: 5432, AutoStart: true,
		}},
	}}}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected config: %#v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestPortForwardValidation(t *testing.T) {
	valid := PortForward{Name: "proxy", Type: ForwardDynamic, BindHost: "127.0.0.1", BindPort: 1080}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	invalid := PortForward{Name: "db", Type: ForwardLocal, BindHost: "127.0.0.1", BindPort: 5433}
	if err := invalid.Validate(); err == nil {
		t.Fatal("expected missing target to fail validation")
	}
	session := Session{Name: "x", Host: "h", Port: 22, User: "u", PortForwards: []PortForward{valid, valid}}
	if err := session.Validate(); err == nil {
		t.Fatal("expected duplicate forward names to fail validation")
	}
}

func TestValidate(t *testing.T) {
	if err := (Session{Name: "x", Host: "h", User: "u", Port: 22}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Session{Name: "x", Host: "h", User: "u"}).Validate(); err == nil {
		t.Fatal("expected invalid port")
	}
}

func TestResolveSessionUsesCredentialProfile(t *testing.T) {
	cfg := Config{
		UserProfiles: []UserProfile{{Name: "production", User: "root", Password: "secret", KeyPath: "/key"}},
		Sessions:     []Session{{Name: "host", Host: "example", Port: 22, CredentialProfile: "production"}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	resolved, err := cfg.ResolveSession(cfg.Sessions[0])
	if err != nil {
		t.Fatal(err)
	}
	if resolved.User != "root" || resolved.Password != "secret" || resolved.KeyPath != "/key" {
		t.Fatalf("resolved session = %#v", resolved)
	}
}

func TestConfigRejectsMissingAndDuplicateCredentialProfiles(t *testing.T) {
	cfg := Config{Sessions: []Session{{Name: "host", Host: "example", Port: 22, CredentialProfile: "missing"}}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("missing credential profile was accepted")
	}
	cfg = Config{UserProfiles: []UserProfile{{Name: "root", User: "root"}, {Name: "ROOT", User: "admin"}}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("duplicate credential profiles were accepted")
	}
}
