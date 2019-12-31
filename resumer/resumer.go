// Package resumer handles auto-resume of failed tasks
package resumer

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"strings"
	"sync/atomic"
	"time"
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
	if err := os.MkdirAll(location, 0700); err != nil {
		log.Printf("[DEBUG] can't make %s, %s", location, err)
	}
	return &Resumer{location: location, enabled: enabled}
}

// OnStart makes a file for started cmd as dt-seq.cronn
func (r *Resumer) OnStart(cmd string) (string, error) {
	seq := atomic.AddUint64(&r.seq, 1)
	fname := fmt.Sprintf("%s/%d-%d.cronn", r.location, time.Now().UnixNano(), seq)
	log.Printf("[DEBUG] create resumer file %s", fname)
	return fname, ioutil.WriteFile(fname, []byte(cmd), 0600)
}

// OnFinish removes cronn fileÒ
func (r *Resumer) OnFinish(fname string) error {
	log.Printf("[DEBUG] detete resumer file %s", fname)
	return os.Remove(fname)
}

// List resumer files and filter old files from this result
func (r *Resumer) List() (res []Cmd) {
	if !r.enabled {
		return []Cmd{}
	}

	ff, err := ioutil.ReadDir(r.location)
	if err != nil {
		log.Printf("[WARN] can't get resume list for %s, %s", r.location, err)
		return []Cmd{}
	}

	for _, finfo := range ff {
		if finfo.IsDir() {
			continue
		}
		if !strings.HasSuffix(finfo.Name(), ".cronn") {
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
		data, err := ioutil.ReadFile(fileName)
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
