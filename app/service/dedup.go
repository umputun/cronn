package service

import (
	"sync"
	"time"
)

// DeDup implements thread safe map to register/unregister command in order to prevent dbl registration
type DeDup struct {
	active  map[string]time.Time
	lock    sync.Mutex
	enabled bool
}

// NewDeDup creates DeDup. Object safe to use with default params (disabled)
func NewDeDup(enabled bool) *DeDup {
	return &DeDup{active: make(map[string]time.Time), enabled: enabled}
}

// Add command to the map, fail if already in
func (d *DeDup) Add(cmd string) bool {
	if !d.enabled {
		return true
	}
	d.lock.Lock()
	defer d.lock.Unlock()
	if _, found := d.active[cmd]; found {
		return false
	}
	d.active[cmd] = time.Now()
	return true
}

// Remove command from the map. Safe to call multiple times
func (d *DeDup) Remove(cmd string) {
	if !d.enabled {
		return
	}
	d.lock.Lock()
	defer d.lock.Unlock()
	delete(d.active, cmd)
}
