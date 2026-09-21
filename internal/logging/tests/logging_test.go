package tests

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"interview/internal/logging"
)

func config(dir string) logging.Config {
	return logging.Config{Dir: dir, Level: "info", MaxSizeMB: 10, MaxBackups: 3, MaxAgeDays: 7, Compress: false}
}

// newLogger returns a logger, the console output it wrote to, and its close func.
func newLogger(t *testing.T, cfg logging.Config) (*slog.Logger, *bytes.Buffer, func()) {
	t.Helper()
	var console bytes.Buffer
	log, closeFile, err := logging.New(cfg, &console)
	if err != nil {
		t.Fatalf("logging.New: %v", err)
	}
	return log, &console, closeFile
}

// fileRecords reads the JSON lines of a log file.
func fileRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var records []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var rec map[string]any
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q: %v", sc.Text(), err)
		}
		records = append(records, rec)
	}
	return records
}

func logFile(dir string) string { return filepath.Join(dir, logging.FileName) }

func TestNew_WritesTextToTheConsoleAndJSONToTheFile(t *testing.T) {
	dir := t.TempDir()
	log, console, closeFile := newLogger(t, config(dir))
	defer closeFile()

	log.Info("payment webhook", "loan_id", 7, "status", 200)

	if out := console.String(); !strings.Contains(out, `msg="payment webhook"`) || !strings.Contains(out, "loan_id=7") {
		t.Errorf("console output is not the readable text format: %q", out)
	}
	records := fileRecords(t, logFile(dir))
	if len(records) != 1 {
		t.Fatalf("file has %d records, want 1", len(records))
	}
	rec := records[0]
	if rec["msg"] != "payment webhook" || rec["level"] != "INFO" || rec["loan_id"] != float64(7) || rec["status"] != float64(200) || rec["time"] == nil {
		t.Errorf("file record = %v", rec)
	}
}

func TestNew_CreatesAMissingLogDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b", "logs")
	_, _, closeFile := newLogger(t, config(dir))
	defer closeFile()

	if _, err := os.Stat(logFile(dir)); err != nil {
		t.Fatalf("log file was not created up front: %v", err)
	}
}

func TestNew_LogsSurviveARestart(t *testing.T) {
	dir := t.TempDir()

	log, _, closeFile := newLogger(t, config(dir))
	log.Info("first run")
	closeFile()

	log, _, closeFile = newLogger(t, config(dir))
	log.Info("second run")
	closeFile()

	records := fileRecords(t, logFile(dir))
	if len(records) != 2 || records[0]["msg"] != "first run" || records[1]["msg"] != "second run" {
		t.Fatalf("records after a restart = %v, want both runs in order", records)
	}
}

func TestNew_LevelFiltersBothOutputs(t *testing.T) {
	dir := t.TempDir()
	cfg := config(dir)
	cfg.Level = "warn"
	log, console, closeFile := newLogger(t, cfg)
	defer closeFile()

	log.Debug("debug line")
	log.Info("info line")
	log.Warn("warn line")
	log.Error("error line")

	records := fileRecords(t, logFile(dir))
	if len(records) != 2 || records[0]["msg"] != "warn line" || records[1]["msg"] != "error line" {
		t.Errorf("file records = %v, want only warn and error", records)
	}
	if out := console.String(); strings.Contains(out, "info line") || !strings.Contains(out, "warn line") {
		t.Errorf("console = %q, want only warn and error", out)
	}
}

func TestNew_AttributesAndGroupsReachBothOutputs(t *testing.T) {
	dir := t.TempDir()
	log, console, closeFile := newLogger(t, config(dir))
	defer closeFile()

	log.With("company_id", 3).WithGroup("req").Info("handled", "id", 1)

	rec := fileRecords(t, logFile(dir))[0]
	req, _ := rec["req"].(map[string]any)
	if rec["company_id"] != float64(3) || req["id"] != float64(1) {
		t.Errorf("file record = %v", rec)
	}
	if out := console.String(); !strings.Contains(out, "company_id=3") || !strings.Contains(out, "req.id=1") {
		t.Errorf("console = %q", out)
	}
}

func TestNew_RotatesInsteadOfGrowingForever(t *testing.T) {
	dir := t.TempDir()
	cfg := config(dir)
	cfg.MaxSizeMB = 1
	log, _, closeFile := newLogger(t, cfg)

	const lines = 1500 // about 1.5 MB, so the 1 MB limit is crossed once
	padding := strings.Repeat("x", 1000)
	for i := 0; i < lines; i++ {
		log.Info("filler", "n", i, "padding", padding)
	}
	closeFile()

	backups, err := filepath.Glob(filepath.Join(dir, "app-*.log"))
	if err != nil || len(backups) == 0 {
		t.Fatalf("no rotated file next to app.log (glob err %v)", err)
	}
	total := len(fileRecords(t, logFile(dir)))
	for _, b := range backups {
		total += len(fileRecords(t, b))
	}
	if total != lines {
		t.Fatalf("%d records across the active and rotated files, want all %d (rotation must not lose logs)", total, lines)
	}
}

func TestNew_LogFileIsPrivateToTheOwner(t *testing.T) {
	dir := t.TempDir()
	_, _, closeFile := newLogger(t, config(dir))
	defer closeFile()

	info, err := os.Stat(logFile(dir))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("log file mode = %v, want 0600 (logs carry business data)", perm)
	}
}

func TestNew_FailsFastWhenTheLogDirectoryCannotBeCreated(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("i am a file"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := logging.New(config(filepath.Join(blocker, "logs")), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "log directory") {
		t.Fatalf("got %v, want an error about the log directory", err)
	}
}

func TestNew_RejectsAnUnknownLevel(t *testing.T) {
	cfg := config(t.TempDir())
	cfg.Level = "loud"

	if _, _, err := logging.New(cfg, &bytes.Buffer{}); err == nil {
		t.Fatal("expected an error for an unknown level")
	}
}

func TestParseLevel(t *testing.T) {
	tests := map[string]slog.Level{
		"debug": slog.LevelDebug, "INFO": slog.LevelInfo, " Warn ": slog.LevelWarn,
		"warning": slog.LevelWarn, "error": slog.LevelError,
	}
	for in, want := range tests {
		got, err := logging.ParseLevel(in)
		if err != nil || got != want {
			t.Errorf("ParseLevel(%q) = (%v, %v), want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "trace", "info+2", "verbose"} {
		if _, err := logging.ParseLevel(bad); err == nil {
			t.Errorf("ParseLevel(%q) should fail", bad)
		}
	}
}
