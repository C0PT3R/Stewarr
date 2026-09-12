// Package applog provides Stewarr's mandatory persistent application log.
package applog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type DailyFile struct {
	mu        sync.Mutex
	directory string
	prefix    string
	retention int
	now       func() time.Time
	date      string
	file      *os.File
	errors    chan error
	failure   error
}

func OpenDaily(directory, prefix string, retention int, now func() time.Time) (*DailyFile, error) {
	if strings.TrimSpace(directory) == "" || strings.TrimSpace(prefix) == "" || retention < 1 || now == nil {
		return nil, fmt.Errorf("invalid application log configuration")
	}
	if err := os.MkdirAll(directory, 0o775); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	writer := &DailyFile{directory: directory, prefix: prefix, retention: retention, now: now, errors: make(chan error, 1)}
	if err := writer.openDate(now().Format("2006-01-02")); err != nil {
		return nil, err
	}
	if err := writer.removeExpired(); err != nil {
		_ = writer.file.Close()
		return nil, err
	}
	return writer, nil
}

func (writer *DailyFile) logPath(date string) string {
	return filepath.Join(writer.directory, writer.prefix+"-"+date+".log")
}

func (writer *DailyFile) openDate(date string) error {
	file, err := os.OpenFile(writer.logPath(date), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o664)
	if err != nil {
		return fmt.Errorf("open application log for %s: %w", date, err)
	}
	writer.file = file
	writer.date = date
	return nil
}

func (writer *DailyFile) removeExpired() error {
	matches, err := filepath.Glob(filepath.Join(writer.directory, writer.prefix+"-????-??-??.log"))
	if err != nil {
		return fmt.Errorf("enumerate application logs: %w", err)
	}
	sort.Strings(matches)
	for len(matches) > writer.retention {
		if err := os.Remove(matches[0]); err != nil {
			return fmt.Errorf("remove expired application log %s: %w", filepath.Base(matches[0]), err)
		}
		matches = matches[1:]
	}
	return nil
}

func (writer *DailyFile) reportFailure(err error) {
	if writer.failure != nil {
		return
	}
	writer.failure = err
	select {
	case writer.errors <- err:
	default:
	}
}

func (writer *DailyFile) Write(content []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.failure != nil {
		return 0, writer.failure
	}
	date := writer.now().Format("2006-01-02")
	if date != writer.date {
		if err := writer.file.Close(); err != nil {
			wrapped := fmt.Errorf("close application log at midnight: %w", err)
			writer.reportFailure(wrapped)
			return 0, wrapped
		}
		if err := writer.openDate(date); err != nil {
			writer.reportFailure(err)
			return 0, err
		}
		if err := writer.removeExpired(); err != nil {
			writer.reportFailure(err)
			return 0, err
		}
	}
	written, err := writer.file.Write(content)
	if err == nil && written != len(content) {
		err = fmt.Errorf("short application log write: wrote %d of %d bytes", written, len(content))
	}
	if err != nil {
		writer.reportFailure(err)
	}
	return written, err
}

func (writer *DailyFile) Errors() <-chan error { return writer.errors }

func (writer *DailyFile) Err() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.failure
}

func (writer *DailyFile) Close() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.file == nil {
		return nil
	}
	return writer.file.Close()
}
