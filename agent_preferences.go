package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const agentRecentLimit = 6

// agentPreferencesVersion 2 introduced one global project folder shared by
// every harness. Projects then only holds explicit per-harness overrides.
const agentPreferencesVersion = 2

// UI preferences are independent of credentials and generated agent profiles.
type agentPreferences struct {
	Version int `json:"version,omitempty"`
	// Folder is the project folder harnesses open in unless overridden.
	Folder string `json:"folder,omitempty"`
	// Projects maps a harness to the folder it uses instead of Folder.
	Projects map[string]string `json:"projects"`
	Recent   []string          `json:"recent,omitempty"`
	// RecentUsed records when each recent folder was last chosen or opened (Unix seconds).
	RecentUsed map[string]int64 `json:"recentUsed,omitempty"`
	// LastOpened records when each harness was last opened from Kilo Proxy (Unix seconds).
	LastOpened   map[string]int64 `json:"lastOpened,omitempty"`
	CodexAppPath string           `json:"codexAppPath,omitempty"`
}

func readAgentPreferences(dir string) (agentPreferences, error) {
	p := agentPreferences{Projects: map[string]string{}}
	data, err := os.ReadFile(filepath.Join(dir, "agent-preferences.json"))
	if errors.Is(err, os.ErrNotExist) {
		p.Version = agentPreferencesVersion
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
	// Older installations can remember a Cursor project. Ignore that retired
	// integration without discarding the other agents' folders or app paths.
	delete(p.Projects, "cursor")
	if err = validateAgentPreferences(p); err != nil {
		return agentPreferences{Projects: map[string]string{}}, err
	}
	p.migrate()
	return p, nil
}

// migrate turns version 1 preferences, where every harness remembered the
// folder it last opened, into one global folder plus overrides. The most
// recently used folder becomes global; a harness keeps its own folder only
// when it differs, so every harness still opens where it did before.
func (p *agentPreferences) migrate() {
	if p.Version >= agentPreferencesVersion {
		return
	}
	p.Version = agentPreferencesVersion
	if p.Folder == "" && len(p.Recent) > 0 {
		p.Folder = p.Recent[0]
	}
	if p.Folder == "" {
		keys := make([]string, 0, len(p.Projects))
		for key := range p.Projects {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if p.Projects[key] != "" {
				p.Folder = p.Projects[key]
				break
			}
		}
	}
	for key, path := range p.Projects {
		if path == "" || path == p.Folder {
			delete(p.Projects, key)
		}
	}
}

func validAgentPreferencePath(path string) bool {
	return path == "" || len(path) <= 8192 && filepath.IsAbs(path) && !strings.ContainsAny(path, "\x00\r\n")
}

func validateAgentPreferences(p agentPreferences) error {
	if len(p.Recent) > agentRecentLimit || !validAgentPreferencePath(p.CodexAppPath) || !validAgentPreferencePath(p.Folder) || len(p.RecentUsed) > agentRecentLimit {
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
	for path, at := range p.RecentUsed {
		if path == "" || !validAgentPreferencePath(path) || at < 0 {
			return errors.New("Invalid recent project folder")
		}
	}
	for key, at := range p.LastOpened {
		if name, _ := launchClientIdentity(key); name == "" || at < 0 {
			return errors.New("Invalid harness history")
		}
	}
	return nil
}

// rememberProject sets a per-harness folder override and records it as recent.
// An empty directory removes the override.
func (p *agentPreferences) rememberProject(key, directory string) {
	if p.Projects == nil {
		p.Projects = map[string]string{}
	}
	if directory == "" {
		delete(p.Projects, key)
		return
	}
	p.Projects[key] = directory
	p.noteRecent(directory, time.Now())
}

// useFolder makes directory the global project folder.
func (p *agentPreferences) useFolder(directory string, now time.Time) {
	p.Folder = directory
	p.noteRecent(directory, now)
}

// noteOpened records a successful launch: when the harness was opened and,
// for harnesses that open a project, the folder it used.
func (p *agentPreferences) noteOpened(key, directory string, now time.Time) {
	if p.LastOpened == nil {
		p.LastOpened = map[string]int64{}
	}
	p.LastOpened[key] = now.Unix()
	if directory != "" {
		p.noteRecent(directory, now)
	}
}

func (p *agentPreferences) noteRecent(directory string, now time.Time) {
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
	used := map[string]int64{directory: now.Unix()}
	for _, path := range recent[1:] {
		if at, ok := p.RecentUsed[path]; ok {
			used[path] = at
		}
	}
	p.RecentUsed = used
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
