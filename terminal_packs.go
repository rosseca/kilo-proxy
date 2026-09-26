package main

import (
	"errors"
	"fmt"
)

// The terminal commands are prepared by the local service, independently of
// whether its native window is open. Read the same saved agent assignment as
// the native launcher so kilo-codex/claude/omp/opencode honor pack switches.
// Caller holds app.mu.
func (a *app) terminalModelLibrary(client string, shared modelLibrary) (modelLibrary, error) {
	packs := newModelPacksStore(a.dir)
	if packs.recovery {
		return modelLibrary{}, errors.New("Saved packs need recovery. Open Models → Packs in Kilo Proxy before launching this agent.")
	}
	id := packs.value.Assignments[client]
	if id == "" {
		return shared, nil
	}
	catalog := readNativeCatalogCache(a.dir, a.config.OrgID)
	library, err := modelPackLibrary(packs.value, id, catalog)
	if err != nil {
		return modelLibrary{}, err
	}
	if len(library.Models) == 0 {
		return modelLibrary{}, errors.New("The selected model pack is empty. Edit it in Models before opening this agent.")
	}
	known := make(map[string]bool, len(catalog))
	for _, model := range catalog {
		known[model.ID] = true
	}
	for _, item := range library.Models {
		if !known[item.ID] {
			return modelLibrary{}, fmt.Errorf("Pack model %s is not in the saved team catalog. Refresh Models before launching this agent.", item.ID)
		}
	}
	return library, nil
}
