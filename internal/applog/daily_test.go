package applog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDailyFileAppendsAndRollsAtLocalMidnight(t *testing.T) {
	directory := t.TempDir()
	current := time.Date(2026, 8, 29, 23, 59, 0, 0, time.FixedZone("EDT", -4*60*60))
	now := func() time.Time { return current }
	writer, err := OpenDaily(directory, "stewarr", 10, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("first\n")); err != nil {
		t.Fatal(err)
	}
	current = current.Add(2 * time.Minute)
	if _, err := writer.Write([]byte("second\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	for name, expected := range map[string]string{"stewarr-2026-08-29.log": "first\n", "stewarr-2026-08-30.log": "second\n"} {
		content, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || string(content) != expected {
			t.Fatalf("%s=%q err=%v", name, content, err)
		}
	}
}

func TestDailyFileRetainsLatestTenDays(t *testing.T) {
	directory := t.TempDir()
	for day := 1; day <= 11; day++ {
		name := filepath.Join(directory, time.Date(2026, 8, day, 0, 0, 0, 0, time.UTC).Format("stewarr-2006-01-02.log"))
		if err := os.WriteFile(name, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writer, err := OpenDaily(directory, "stewarr", 10, func() time.Time { return time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	matches, _ := filepath.Glob(filepath.Join(directory, "stewarr-*.log"))
	if len(matches) != 10 || strings.HasSuffix(matches[0], "2026-08-01.log") {
		t.Fatalf("retained=%v", matches)
	}
}

func TestDailyFileRequiresWritableLogPath(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(parent, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenDaily(filepath.Join(parent, "log"), "stewarr", 10, time.Now); err == nil || !strings.Contains(err.Error(), "log directory") {
		t.Fatalf("error=%v", err)
	}
}

func TestDailyFileReportsRuntimeWriteFailure(t *testing.T) {
	writer, err := OpenDaily(t.TempDir(), "stewarr", 10, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("cannot persist\n")); err == nil {
		t.Fatal("write to closed mandatory log succeeded")
	}
	select {
	case reported := <-writer.Errors():
		if reported == nil || writer.Err() == nil {
			t.Fatalf("reported=%v stored=%v", reported, writer.Err())
		}
	default:
		t.Fatal("runtime log failure was not reported")
	}
}
