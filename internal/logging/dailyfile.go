package logging

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/srjn45/wdx/internal/identity"
)

var filePrefix = identity.Name + "-"

const fileSuffix = ".log"

// DailyFile writes to one file per local day in a directory and deletes the
// files older than a number of days. Files are only ever appended to, never
// renamed, so many processes can write to the same day's file at once.
//
// When the directory or file cannot be opened, writes are discarded until the
// next day: logging never fails what is being logged.
type DailyFile struct {
	dir           string
	retentionDays int
	now           func() time.Time

	mu   sync.Mutex
	day  string
	file *os.File
}

// NewDailyFile returns a DailyFile writing to dir that keeps the files of the
// last retentionDays days besides today's. now tells the day.
func NewDailyFile(dir string, retentionDays int, now func() time.Time) *DailyFile {
	return &DailyFile{dir: dir, retentionDays: retentionDays, now: now}
}

func (d *DailyFile) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if day := d.now().Format(time.DateOnly); day != d.day {
		d.open(day)
	}
	if d.file == nil {
		return len(p), nil
	}
	return d.file.Write(p)
}

// Close closes the current file.
func (d *DailyFile) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.file == nil {
		return nil
	}
	err := d.file.Close()
	d.file = nil
	return err
}

func (d *DailyFile) open(day string) {
	if d.file != nil {
		d.file.Close()
		d.file = nil
	}
	d.day = day
	if err := os.MkdirAll(d.dir, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(d.dir, filePrefix+day+fileSuffix), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return
	}
	d.file = f
	d.deleteOld()
}

// deleteOld deletes the log files older than the retention period. Another
// process may delete the same files at the same time; that is not an error.
func (d *DailyFile) deleteOld() {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return
	}
	cutoff := d.now().AddDate(0, 0, -d.retentionDays).Format(time.DateOnly)
	for _, e := range entries {
		day, ok := strings.CutPrefix(e.Name(), filePrefix)
		day, isLog := strings.CutSuffix(day, fileSuffix)
		if !ok || !isLog || !e.Type().IsRegular() {
			continue
		}
		if _, err := time.Parse(time.DateOnly, day); err == nil && day < cutoff {
			os.Remove(filepath.Join(d.dir, e.Name()))
		}
	}
}
