package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const fileName = ".ssh-manager-tui.json"

type Session struct {
	Name              string        `json:"name"`
	Host              string        `json:"host"`
	Port              int           `json:"port"`
	User              string        `json:"user"`
	Password          string        `json:"password,omitempty"`
	KeyPath           string        `json:"key_path,omitempty"`
	CredentialProfile string        `json:"credential_profile,omitempty"`
	Group             string        `json:"group,omitempty"`
	ConnectionCount   uint64        `json:"connection_count,omitempty"`
	LastConnected     time.Time     `json:"last_connected,omitzero"`
	PortForwards      []PortForward `json:"port_forwards,omitempty"`
}

type UserProfile struct {
	Name     string `json:"name"`
	User     string `json:"user"`
	Password string `json:"password,omitempty"`
	KeyPath  string `json:"key_path,omitempty"`
}

func (p UserProfile) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("profile name is required")
	}
	if strings.TrimSpace(p.User) == "" {
		return errors.New("profile user is required")
	}
	if strings.IndexFunc(p.Name, unicode.IsControl) >= 0 {
		return errors.New("profile name must not contain control characters")
	}
	return nil
}

type ForwardType string

const (
	ForwardLocal   ForwardType = "local"
	ForwardRemote  ForwardType = "remote"
	ForwardDynamic ForwardType = "dynamic"
)

type PortForward struct {
	Name       string      `json:"name"`
	Type       ForwardType `json:"type"`
	BindHost   string      `json:"bind_host"`
	BindPort   int         `json:"bind_port"`
	TargetHost string      `json:"target_host,omitempty"`
	TargetPort int         `json:"target_port,omitempty"`
	AutoStart  bool        `json:"auto_start,omitempty"`
}

func (f PortForward) Validate() error {
	if strings.TrimSpace(f.Name) == "" {
		return errors.New("forward name is required")
	}
	switch f.Type {
	case ForwardLocal, ForwardRemote, ForwardDynamic:
	default:
		return fmt.Errorf("unsupported forward type %q", f.Type)
	}
	if strings.TrimSpace(f.BindHost) == "" {
		return errors.New("bind host is required")
	}
	if f.BindPort < 1 || f.BindPort > 65535 {
		return errors.New("bind port must be between 1 and 65535")
	}
	if f.Type != ForwardDynamic {
		if strings.TrimSpace(f.TargetHost) == "" {
			return errors.New("target host is required")
		}
		if f.TargetPort < 1 || f.TargetPort > 65535 {
			return errors.New("target port must be between 1 and 65535")
		}
	}
	return nil
}

func (s Session) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("session name is required")
	}
	if strings.TrimSpace(s.Host) == "" {
		return errors.New("host is required")
	}
	if strings.TrimSpace(s.User) == "" && strings.TrimSpace(s.CredentialProfile) == "" {
		return errors.New("user is required")
	}
	if s.Port < 1 || s.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if strings.IndexFunc(s.Group, unicode.IsControl) >= 0 {
		return errors.New("group must not contain control characters")
	}
	forwardNames := make(map[string]struct{}, len(s.PortForwards))
	for _, forward := range s.PortForwards {
		if err := forward.Validate(); err != nil {
			return fmt.Errorf("invalid port forward %q: %w", forward.Name, err)
		}
		name := strings.ToLower(strings.TrimSpace(forward.Name))
		if _, exists := forwardNames[name]; exists {
			return fmt.Errorf("duplicate port forward name %q", forward.Name)
		}
		forwardNames[name] = struct{}{}
	}
	return nil
}

type Config struct {
	Sessions     []Session     `json:"sessions"`
	UserProfiles []UserProfile `json:"user_profiles,omitempty"`
	Theme        string        `json:"theme,omitempty"`
}

func (c Config) UserProfile(name string) (UserProfile, bool) {
	name = strings.TrimSpace(name)
	for _, profile := range c.UserProfiles {
		if strings.EqualFold(profile.Name, name) {
			return profile, true
		}
	}
	return UserProfile{}, false
}

func (c Config) ResolveSession(session Session) (Session, error) {
	if strings.TrimSpace(session.CredentialProfile) == "" {
		if err := session.Validate(); err != nil {
			return Session{}, err
		}
		return session, nil
	}
	profile, ok := c.UserProfile(session.CredentialProfile)
	if !ok {
		return Session{}, fmt.Errorf("credential profile %q not found", session.CredentialProfile)
	}
	resolved := session
	resolved.CredentialProfile = profile.Name
	resolved.User = profile.User
	resolved.Password = profile.Password
	resolved.KeyPath = profile.KeyPath
	if err := resolved.Validate(); err != nil {
		return Session{}, err
	}
	return resolved, nil
}

func (c Config) Validate() error {
	profileNames := make(map[string]struct{}, len(c.UserProfiles))
	for _, profile := range c.UserProfiles {
		if err := profile.Validate(); err != nil {
			return fmt.Errorf("invalid user profile %q: %w", profile.Name, err)
		}
		name := strings.ToLower(strings.TrimSpace(profile.Name))
		if _, exists := profileNames[name]; exists {
			return fmt.Errorf("duplicate user profile %q", profile.Name)
		}
		profileNames[name] = struct{}{}
	}
	for _, session := range c.Sessions {
		if err := session.Validate(); err != nil {
			return fmt.Errorf("invalid session %q: %w", session.Name, err)
		}
		if session.CredentialProfile != "" {
			if _, ok := c.UserProfile(session.CredentialProfile); !ok {
				return fmt.Errorf("invalid session %q: credential profile %q not found", session.Name, session.CredentialProfile)
			}
		}
	}
	return nil
}

type Store struct {
	path string
}

func NewStore() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("locate home directory: %w", err)
	}
	return &Store{path: filepath.Join(home, fileName)}, nil
}

func NewStoreAt(path string) *Store { return &Store{path: path} }

func (s *Store) Path() string { return s.path }

func (s *Store) Load() (Config, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", s.path, err)
	}
	if err := os.Chmod(s.path, 0o600); err != nil {
		return Config{}, fmt.Errorf("secure %s: %w", s.path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", s.path, err)
	}
	return cfg, nil
}

func (s *Store) Save(cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".ssh-manager-tui-*")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return os.Chmod(s.path, 0o600)
}
