package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const agentRecentLimit = 6

// UI preferences are independent of credentials and generated agent profiles.
type agentPreferences struct {
	Projects     map[string]string `json:"projects"`
	Recent       []string          `json:"recent,omitempty"`
	CodexAppPath string            `json:"codexAppPath,omitempty"`
}

func readAgentPreferences(dir string) (agentPreferences, error) {
	p := agentPreferences{Projects: map[string]string{}}
	data, err := os.ReadFile(filepath.Join(dir, "agent-preferences.json"))
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(data, &p); err != nil {
		return agentPreferences{Projects: map[string]string{}}, err
	}
	if p.Projects == nil {
		p.Projects = map[string]string{}
	}
	if err = validateAgentPreferences(p); err != nil {
		return agentPreferences{Projects: map[string]string{}}, err
	}
	return p, nil
}

func validAgentPreferencePath(path string) bool {
	return path == "" || len(path) <= 8192 && filepath.IsAbs(path) && !strings.ContainsAny(path, "\x00\r\n")
}

func validateAgentPreferences(p agentPreferences) error {
	if len(p.Recent) > agentRecentLimit || !validAgentPreferencePath(p.CodexAppPath) {
		return errors.New("Invalid agent preferences")
	}
	for key, path := range p.Projects {
		name, _ := launchClientIdentity(key)
		if name == "" || !validAgentPreferencePath(path) {
			return errors.New("Invalid remembered agent project")
		}
	}
	for _, path := range p.Recent {
		if path == "" || !validAgentPreferencePath(path) {
			return errors.New("Invalid recent project folder")
		}
	}
	return nil
}

func (p *agentPreferences) rememberProject(key, directory string) {
	if p.Projects == nil {
		p.Projects = map[string]string{}
	}
	p.Projects[key] = directory
	if directory == "" {
		return
	}
	recent := []string{directory}
	for _, existing := range p.Recent {
		if existing != directory && len(recent) < agentRecentLimit {
			recent = append(recent, existing)
		}
	}
	p.Recent = recent
}

func writeAgentPreferences(dir string, p agentPreferences) error {
	if err := validateAgentPreferences(p); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".agent-preferences-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "agent-preferences.json"))
}
