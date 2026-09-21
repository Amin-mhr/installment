package tests

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"gorm.io/gorm"

	"interview/internal/repo"
	"interview/internal/testutil"
)

func openWithLogger(t *testing.T, log *slog.Logger) *gorm.DB {
	t.Helper()
	db, err := repo.Open(testutil.NewDSN(t), log)
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
	return db
}

func TestOpen_LogsDatabaseErrorsToTheAppLoggerWithoutQueryParameters(t *testing.T) {
	var logs bytes.Buffer
	db := openWithLogger(t, slog.New(slog.NewJSONHandler(&logs, nil)))

	const distinctive = 918273645
	if err := db.Exec("SELECT ?::int / 0", distinctive).Error; err == nil {
		t.Fatal("expected a division by zero error")
	}

	out := logs.String()
	if !strings.Contains(out, `"msg":"database"`) || !strings.Contains(out, "division by zero") {
		t.Errorf("the database error did not reach the app logger:\n%s", out)
	}
	if strings.Contains(out, "918273645") {
		t.Errorf("a query parameter leaked into the log:\n%s", out)
	}
}

func TestOpen_ANilLoggerDiscardsGormOutputInsteadOfPanicking(t *testing.T) {
	db := openWithLogger(t, nil)

	if err := db.Exec("SELECT 1 / 0").Error; err == nil {
		t.Fatal("expected a division by zero error")
	}
}
