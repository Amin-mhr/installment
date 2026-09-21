package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"interview/internal/domain"
	"interview/internal/interfaces"
	"interview/internal/repo"
	"interview/internal/service"
	"interview/internal/testutil"
)

type loanEnv struct {
	svc       *service.LoanService
	db        *gorm.DB
	userID    int64
	companyID int64
}

func newLoanEnv(t *testing.T, installments func(interfaces.InstallmentRepository) interfaces.InstallmentRepository) loanEnv {
	t.Helper()
	db := testutil.NewDB(t)

	var inst interfaces.InstallmentRepository = repo.NewInstallmentRepo(db)
	if installments != nil {
		inst = installments(inst)
	}
	return loanEnv{
		svc:       service.NewLoanService(repo.NewLoanRepo(db), inst, repo.NewTxManager(db)),
		db:        db,
		userID:    testutil.SeedUser(t, db),
		companyID: testutil.SeedCompany(t, db, "secret"),
	}
}

func TestCreateLoan_WritesOneInstallmentPerMonthOfTerm(t *testing.T) {
	for _, term := range []int{4, 12, 18} {
		env := newLoanEnv(t, nil)

		total := int64(term)*1000 + 7
		loan, err := env.svc.CreateLoan(context.Background(), service.CreateLoanCommand{
			UserID: env.userID, CreditCompanyID: env.companyID, TotalAmount: total, TermMonths: term,
			StartDate: time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC),
		})
		if err != nil {
			t.Fatalf("term %d: %v", term, err)
		}

		rows := testutil.LoanInstallments(t, env.db, loan.ID)
		if len(rows) != term {
			t.Fatalf("term %d: got %d installments", term, len(rows))
		}
		var sum int64
		for i, row := range rows {
			sum += row.Amount
			if row.Number != i+1 || row.Status != "pending" {
				t.Errorf("term %d: row %d = %+v", term, i, row)
			}
		}
		if sum != total {
			t.Errorf("term %d: installments sum to %d, want %d", term, sum, total)
		}
		if n := testutil.CountRows(t, env.db, "loans"); n != 1 {
			t.Errorf("term %d: %d loan rows, want 1", term, n)
		}
	}
}

func TestCreateLoan_InvalidInputWritesNothing(t *testing.T) {
	env := newLoanEnv(t, nil)

	_, err := env.svc.CreateLoan(context.Background(), service.CreateLoanCommand{
		UserID: env.userID, CreditCompanyID: env.companyID, TotalAmount: 1000, TermMonths: 0, StartDate: time.Now(),
	})
	if !errors.Is(err, domain.ErrInvalidLoan) {
		t.Fatalf("got %v, want ErrInvalidLoan", err)
	}
	if n := testutil.CountRows(t, env.db, "loans"); n != 0 {
		t.Fatalf("%d loan rows written", n)
	}
}

func TestCreateLoan_UnknownUserOrCompanyWritesNothing(t *testing.T) {
	env := newLoanEnv(t, nil)

	for name, cmd := range map[string]service.CreateLoanCommand{
		"unknown user":    {UserID: 999_999, CreditCompanyID: env.companyID},
		"unknown company": {UserID: env.userID, CreditCompanyID: 999_999},
	} {
		cmd.TotalAmount, cmd.TermMonths, cmd.StartDate = 1200, 12, time.Now()
		if _, err := env.svc.CreateLoan(context.Background(), cmd); err == nil {
			t.Errorf("%s: expected a foreign key error", name)
		}
	}
	if n := testutil.CountRows(t, env.db, "loans") + testutil.CountRows(t, env.db, "installments"); n != 0 {
		t.Fatalf("%d rows written", n)
	}
}

// failingInstallments lets the loan insert succeed, then fails the schedule insert.
type failingInstallments struct {
	interfaces.InstallmentRepository
}

func (failingInstallments) CreateBatch(context.Context, []domain.Installment) error {
	return errors.New("boom")
}

func TestCreateLoan_RollsBackLoanWhenScheduleInsertFails(t *testing.T) {
	env := newLoanEnv(t, func(real interfaces.InstallmentRepository) interfaces.InstallmentRepository {
		return failingInstallments{real}
	})

	_, err := env.svc.CreateLoan(context.Background(), service.CreateLoanCommand{
		UserID: env.userID, CreditCompanyID: env.companyID, TotalAmount: 1200, TermMonths: 12, StartDate: time.Now(),
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if n := testutil.CountRows(t, env.db, "loans"); n != 0 {
		t.Fatalf("loan row survived a failed schedule insert (%d rows)", n)
	}
}
