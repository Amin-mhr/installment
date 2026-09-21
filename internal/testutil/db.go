// Package testutil provides real-Postgres helpers for integration tests.
package testutil

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"interview/internal/repo"
)

// NewDSN creates a fresh, empty schema with the migration applied and returns a
// postgres:// URL whose search_path points at it. The schema is dropped when the
// test ends. The test is skipped unless TEST_DATABASE_URL (a postgres:// URL) is set.
func NewDSN(t *testing.T) string {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}

	admin, err := repo.Open(dsn, nil)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	schema := "t_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		if adminSQL, err := admin.DB(); err == nil {
			adminSQL.Close()
		}
	})

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	scoped := u.String()

	db, err := repo.Open(scoped, nil)
	if err != nil {
		t.Fatalf("connect to test schema: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if _, err := sqlDB.ExecContext(context.Background(), readMigration(t)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	return scoped
}

// OpenDB connects to dsn and closes the pool when the test ends.
func OpenDB(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := repo.Open(dsn, nil)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return db
}

// NewDB returns a GORM handle bound to a fresh schema with the migration applied.
func NewDB(t *testing.T) *gorm.DB {
	t.Helper()
	return OpenDB(t, NewDSN(t))
}

func readMigration(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations", "001_init.sql")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	return string(b)
}

func SeedUser(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var id int64
	err := db.Raw(`INSERT INTO users (full_name, email) VALUES ('Test User', ?) RETURNING id`,
		uuid.NewString()+"@example.com").Scan(&id).Error
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func SeedCompany(t *testing.T, db *gorm.DB, webhookSecret string) int64 {
	t.Helper()
	var id int64
	err := db.Raw(`INSERT INTO credit_companies (name, webhook_secret) VALUES (?, ?) RETURNING id`,
		"company-"+uuid.NewString(), webhookSecret).Scan(&id).Error
	if err != nil {
		t.Fatalf("seed credit company: %v", err)
	}
	return id
}

type InstallmentRow struct {
	ID             int64
	Number         int
	Amount         int64
	DueDate        time.Time
	Status         string
	PaidAt         *time.Time
	PaymentEventID *uuid.UUID
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

const installmentColumns = `id, installment_number AS number, amount, due_date, status, paid_at,
	payment_event_id, created_at, updated_at`

// LoanInstallments reads a loan's installment rows straight from the table, ordered by number.
func LoanInstallments(t *testing.T, db *gorm.DB, loanID int64) []InstallmentRow {
	t.Helper()
	var rows []InstallmentRow
	err := db.Raw(`SELECT `+installmentColumns+` FROM installments WHERE loan_id = ? ORDER BY installment_number`,
		loanID).Scan(&rows).Error
	if err != nil {
		t.Fatalf("read installments: %v", err)
	}
	return rows
}

func GetInstallment(t *testing.T, db *gorm.DB, id int64) InstallmentRow {
	t.Helper()
	var row InstallmentRow
	res := db.Raw(`SELECT `+installmentColumns+` FROM installments WHERE id = ?`, id).Scan(&row)
	if res.Error != nil || res.RowsAffected != 1 {
		t.Fatalf("read installment %d: err=%v rows=%d", id, res.Error, res.RowsAffected)
	}
	return row
}

// PaidNumbers returns the installment numbers of a loan that are paid, ascending.
func PaidNumbers(t *testing.T, db *gorm.DB, loanID int64) []int {
	t.Helper()
	var numbers []int
	err := db.Raw(`SELECT installment_number FROM installments
	               WHERE loan_id = ? AND status = 'paid' ORDER BY installment_number`, loanID).Scan(&numbers).Error
	if err != nil {
		t.Fatalf("read paid installments: %v", err)
	}
	return numbers
}

func LoanCredit(t *testing.T, db *gorm.DB, loanID int64) int64 {
	t.Helper()
	var credit int64
	if err := db.Raw(`SELECT credit_balance FROM loans WHERE id = ?`, loanID).Scan(&credit).Error; err != nil {
		t.Fatalf("read loan credit: %v", err)
	}
	return credit
}

// AssertLedger checks the per-loan invariant: every unit of money received is
// either applied to a paid installment or saved as the loan's credit.
func AssertLedger(t *testing.T, db *gorm.DB, loanID int64) {
	t.Helper()
	var row struct{ Received, Applied, Credit int64 }
	err := db.Raw(`SELECT
	    COALESCE((SELECT SUM(amount) FROM payments WHERE loan_id = ?), 0)::bigint AS received,
	    COALESCE((SELECT SUM(amount) FROM installments WHERE loan_id = ? AND status = 'paid'), 0)::bigint AS applied,
	    (SELECT credit_balance FROM loans WHERE id = ?) AS credit`, loanID, loanID, loanID).Scan(&row).Error
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if row.Received != row.Applied+row.Credit {
		t.Fatalf("ledger broken for loan %d: received %d != applied %d + credit %d",
			loanID, row.Received, row.Applied, row.Credit)
	}
}

func CountRows(t *testing.T, db *gorm.DB, table string) int64 {
	t.Helper()
	var n int64
	if err := db.Raw("SELECT count(*) FROM " + table).Scan(&n).Error; err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}
