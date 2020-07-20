// Package crontab deals with the standard 5-elements crontab input from a file
package crontab

import (
	"context"
	"io/ioutil"
	"os"
	"regexp"
	"strings"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/pkg/errors"
)

// Parser file, thread safe
type Parser struct {
	file        string
	updInterval time.Duration
}

// JobSpec for spec and cmd + params
type JobSpec struct {
	Spec    string
	Command string
}

// New creates Parser for file, but not parsing yet
func New(file string, updInterval time.Duration) *Parser {
	log.Printf("[CRON] crontab file %s, update every %v", file, updInterval)
	return &Parser{file: file, updInterval: updInterval}
}

// List parses crontab and returns lit of jobs
func (p Parser) List() (result []JobSpec, err error) {
	bs, err := ioutil.ReadFile(p.file)
	if err != nil {
		return []JobSpec{}, err
	}
	lines := strings.Split(string(bs), "\n")
	for _, l := range lines {
		if js, err := p.parse(l); err == nil {
			result = append(result, js)
		}
	}
	return result, nil
}

func (p Parser) String() string {
	return p.file
}

func (p Parser) parse(line string) (result JobSpec, err error) {
	if strings.HasPrefix(strings.TrimSpace(line), "#") {
		return JobSpec{}, errors.New("comment line " + line)
	}
	reWhtSpaces := regexp.MustCompile(`[\s\p{Zs}]{2,}`)
	l := strings.TrimSpace(line)
	l = strings.Replace(l, "\t", " ", -1)
	singleSpace := reWhtSpaces.ReplaceAllString(l, " ")
	elems := strings.Split(singleSpace, " ")
	if len(elems) < 6 {
		return JobSpec{}, errors.New("not enough elements in " + line)
	}

	return JobSpec{Spec: strings.Join(elems[:5], " "), Command: strings.Join(elems[5:], " ")}, nil
}

// Changes gets updates channel. Each time crontab file updated and modification time changed
// it will get parsed and the full list of jobs will be sent to the channel. Update checked periodically
// and postponed for short time to prevent changes on every small intermediate save.
func (p Parser) Changes(ctx context.Context) (<-chan []JobSpec, error) {
	ch := make(chan []JobSpec)

	// get modification time of crontab file
	mtime := func() (time.Time, error) {
		st, err := os.Stat(p.file)
		if err != nil {
			return time.Time{}, errors.Wrapf(err, "can't load cron file %s", p.file)
		}
		return st.ModTime(), nil
	}

	lastMtime, err := mtime()
	if err != nil {
		// need file available to start change watcher
		return nil, err
	}

	ticker := time.NewTicker(p.updInterval)
	go func() {
		for {
			select {
			case <-ctx.Done():
				close(ch)
				return
			case <-ticker.C: // react on changes every X seconds
				m, err := mtime()
				if err != nil {
					log.Printf("[WARN] can't get info about %s, %v", p.file, err)
					continue
				}

				secsSinceChange := time.Now().Second() - m.Second()
				secsThreshold := int(p.updInterval.Seconds() / 2)
				if m != lastMtime && secsSinceChange >= secsThreshold {
					// change should be at least X/2 secs old to prevent changes on every small intermediate save
					lastMtime = m
					jobs, err := p.List()
					if err != nil {
						log.Printf("[WARN] can't get list of jobs from %s, %v", p.file, err)
						continue
					}
					ch <- jobs
				}
			}
		}
	}()

	return ch, nil
}
