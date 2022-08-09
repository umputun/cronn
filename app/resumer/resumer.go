// Package resumer handles auto-resume of failed tasks
package resumer

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"sync/atomic"
	"time"

	log "github.com/go-pkgz/lgr"
)

// Resumer keeps track of executed commands in .cronn files
type Resumer struct {
	location string
	enabled  bool
	seq      uint64
}

// Cmd keeps file name and command
type Cmd struct {
	Command string
	Fname   string
}

// New makes resumer for given location. Enabled affects List only
func New(location string, enabled bool) *Resumer {
	if enabled {
		if err := os.MkdirAll(location, 0o700); err != nil {
			log.Printf("[DEBUG] can't make %s, %s", location, err)
		}
	}
	return &Resumer{location: location, enabled: enabled}
}

// OnStart makes a file for started cmd as dt-seq.cronn
func (r *Resumer) OnStart(cmd string) (string, error) {
	if !r.enabled {
		return "", nil
	}
	seq := atomic.AddUint64(&r.seq, 1)
	fname := fmt.Sprintf("%s/%d-%d.cronn", r.location, time.Now().UnixNano(), seq)
	log.Printf("[DEBUG] create resumer file %s", fname)
	return fname, os.WriteFile(fname, []byte(cmd), 0600)
}

// OnFinish removes cronn fileÒ
func (r *Resumer) OnFinish(fname string) error {
	if !r.enabled {
		return nil
	}
	log.Printf("[DEBUG] delete resumer file %s", fname)
	return os.Remove(fname)
}

// List resumer files and filter old files from this result
func (r *Resumer) List() (res []Cmd) {
	if !r.enabled {
		return []Cmd{}
	}

	entries, err := os.ReadDir(r.location)
	if err != nil {
		log.Printf("[WARN] can't get resume list for %s, %s", r.location, err)
		return []Cmd{}
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".cronn") {
			continue
		}

		finfo, err := entry.Info()
		if err != nil {
			log.Printf("[WARN] can't get resume info for %s, %s", entry.Name(), err)
			continue
		}

		fileName := path.Join(r.location, finfo.Name())
		// skip old files
		if finfo.ModTime().Add(24 * time.Hour).Before(time.Now()) {
			log.Printf("[DEBUG] resume file %s too old", fileName)
			if err := os.Remove(fileName); err != nil {
				log.Printf("[WARN] can't delete %s, %s", fileName, err)
			}
			continue
		}
		data, err := ioutil.ReadFile(fileName) // nolint gosec
		if err != nil {
			log.Printf("[WARN] failed to read resume file %s, %s", fileName, err)
			continue
		}
		resEntry := Cmd{Fname: fileName, Command: string(data)}
		log.Printf("[DEBUG] resume entry %+v", resEntry)
		res = append(res, resEntry)
	}
	return res
}

func (r *Resumer) String() string {
	return fmt.Sprintf("enabled:%v, location:%s", r.enabled, r.location)
}
