package tests

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"interview/internal/domain"
	"interview/internal/repo"
	"interview/internal/testutil"
)

var paidAt = time.Date(2026, 2, 14, 10, 30, 0, 0, time.UTC)

type fixture struct {
	db           *gorm.DB
	tx           *repo.TxManager
	loans        *repo.LoanRepo
	installments *repo.InstallmentRepo
	payments     *repo.PaymentRepo
	companies    *repo.CreditCompanyRepo
	userID       int64
	companyID    int64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := testutil.NewDB(t)
	return &fixture{
		db:           db,
		tx:           repo.NewTxManager(db),
		loans:        repo.NewLoanRepo(db),
		installments: repo.NewInstallmentRepo(db),
		payments:     repo.NewPaymentRepo(db),
		companies:    repo.NewCreditCompanyRepo(db),
		userID:       testutil.SeedUser(t, db),
		companyID:    testutil.SeedCompany(t, db, "secret"),
	}
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// createLoan persists a loan and its schedule for the fixture's user.
func (f *fixture) createLoan(t *testing.T, companyID int64, total int64, term int, start time.Time) (*domain.Loan, []domain.Installment) {
	t.Helper()
	loan, err := domain.NewLoan(f.userID, companyID, total, term, start)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.loans.Create(context.Background(), loan); err != nil {
		t.Fatalf("create loan: %v", err)
	}
	schedule := loan.Schedule()
	if err := f.installments.CreateBatch(context.Background(), schedule); err != nil {
		t.Fatalf("create installments: %v", err)
	}
	return loan, schedule
}

// pay applies a payment the way the service does, but with the real repositories.
func (f *fixture) pay(t *testing.T, loan *domain.Loan, amount int64) *domain.Payment {
	t.Helper()
	var payment *domain.Payment
	err := f.tx.WithinTx(context.Background(), func(ctx context.Context) error {
		unpaid, err := f.installments.ListUnpaid(ctx, loan.ID)
		if err != nil {
			return err
		}
		payment, err = loan.ApplyPayment(uuid.New(), amount, paidAt, unpaid)
		if err != nil {
			return err
		}
		if err := f.payments.Create(ctx, payment); err != nil {
			return err
		}
		if err := f.installments.SaveSettled(ctx, payment); err != nil {
			return err
		}
		return f.loans.SaveCreditBalance(ctx, loan)
	})
	if err != nil {
		t.Fatalf("pay %d: %v", amount, err)
	}
	return payment
}

// insertPayment adds a bare payments row with raw SQL, for constraint tests.
func (f *fixture) insertPayment(t *testing.T, companyID, loanID int64) uuid.UUID {
	t.Helper()
	event := uuid.New()
	err := f.db.Exec(`INSERT INTO payments (credit_company_id, event_id, user_id, loan_id, amount, paid_at, credit_balance_after)
	                  VALUES (?, ?, ?, ?, 100, now(), 0)`, companyID, event, f.userID, loanID).Error
	if err != nil {
		t.Fatalf("insert payment: %v", err)
	}
	return event
}

func numbersOf(installments []domain.Installment) []int {
	var out []int
	for _, inst := range installments {
		out = append(out, inst.Number)
	}
	return out
}

func TestCreateBatch_RoundTripsScheduleExactly(t *testing.T) {
	f := newFixture(t)

	loan, schedule := f.createLoan(t, f.companyID, 1000, 12, day(2026, 1, 31))
	if loan.ID == 0 {
		t.Fatal("loan ID was not assigned")
	}

	rows := testutil.LoanInstallments(t, f.db, loan.ID)
	if len(rows) != 12 {
		t.Fatalf("got %d rows, want 12", len(rows))
	}
	for i, row := range rows {
		want := schedule[i]
		if row.Number != want.Number || row.Amount != want.Amount || !row.DueDate.Equal(want.DueDate) || row.Status != "pending" {
			t.Errorf("row %d = %+v, want number %d amount %d due %s pending", i, row, want.Number, want.Amount, want.DueDate.Format("2006-01-02"))
		}
		if row.PaidAt != nil || row.PaymentEventID != nil {
			t.Errorf("row %d should have no payment data: %+v", i, row)
		}
	}
	if !rows[0].DueDate.Equal(day(2026, 2, 28)) || !rows[1].DueDate.Equal(day(2026, 3, 31)) {
		t.Errorf("due dates not month-end clamped: %v, %v", rows[0].DueDate, rows[1].DueDate)
	}
}

func TestCreateBatch_RejectsDuplicateInstallmentNumberWithinLoan(t *testing.T) {
	f := newFixture(t)
	_, schedule := f.createLoan(t, f.companyID, 400, 4, day(2026, 1, 15))

	if err := f.installments.CreateBatch(context.Background(), schedule[:1]); err == nil {
		t.Fatal("expected UNIQUE (loan_id, installment_number) to reject the duplicate")
	}
}

func TestInstallmentPaymentConstraints(t *testing.T) {
	f := newFixture(t)
	otherCompany := testutil.SeedCompany(t, f.db, "other-secret")
	loanA, _ := f.createLoan(t, f.companyID, 400, 4, day(2026, 1, 15))
	loanB, _ := f.createLoan(t, otherCompany, 400, 4, day(2026, 1, 15))
	id := testutil.LoanInstallments(t, f.db, loanA.ID)[0].ID
	eventA := f.insertPayment(t, f.companyID, loanA.ID)
	eventB := f.insertPayment(t, otherCompany, loanB.ID)

	bad := []struct {
		name string
		stmt string
		args []any
	}{
		{"paid without paid_at or payment", `UPDATE installments SET status = 'paid' WHERE id = ?`, []any{id}},
		{"paid without a payment", `UPDATE installments SET status = 'paid', paid_at = now() WHERE id = ?`, []any{id}},
		{"paid without paid_at", `UPDATE installments SET status = 'paid', payment_event_id = ? WHERE id = ?`, []any{eventA, id}},
		{"pending carrying only a payment", `UPDATE installments SET payment_event_id = ? WHERE id = ?`, []any{eventA, id}},
		{"pending carrying only a paid_at", `UPDATE installments SET paid_at = now() WHERE id = ?`, []any{id}},
		{"pending carrying both", `UPDATE installments SET paid_at = now(), payment_event_id = ? WHERE id = ?`, []any{eventA, id}},
		{"unknown status", `UPDATE installments SET status = 'overdue' WHERE id = ?`, []any{id}},
		{"paid by a payment that does not exist", `UPDATE installments SET status = 'paid', paid_at = now(), payment_event_id = gen_random_uuid() WHERE id = ?`, []any{id}},
		{"paid by another company's payment", `UPDATE installments SET status = 'paid', paid_at = now(), payment_event_id = ? WHERE id = ?`, []any{eventB, id}},
	}
	for _, tc := range bad {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			// Each case runs in its own rolled-back transaction so a wrongly accepted
			// statement cannot change what the next case sees.
			tx := f.db.Begin()
			defer tx.Rollback()
			if err := tx.Exec(tc.stmt, tc.args...).Error; err == nil {
				t.Error("expected the database to reject this")
			}
		})
	}

	good := `UPDATE installments SET status = 'paid', paid_at = now(), payment_event_id = ? WHERE id = ?`
	if err := f.db.Exec(good, eventA, id).Error; err != nil {
		t.Errorf("a paid installment pointing at its own company's payment must be accepted: %v", err)
	}
}

func TestMoneyColumnConstraints(t *testing.T) {
	f := newFixture(t)
	loan, _ := f.createLoan(t, f.companyID, 400, 4, day(2026, 1, 15))

	const insertPayment = `INSERT INTO payments (credit_company_id, event_id, user_id, loan_id, amount, paid_at, credit_balance_after)
	                       VALUES (?, gen_random_uuid(), ?, ?, ?, now(), ?)`
	const insertLoan = `INSERT INTO loans (user_id, credit_company_id, total_amount, term_months, start_date)
	                    VALUES (?, ?, 1000, ?, now())`

	bad := []struct {
		name string
		stmt string
		args []any
	}{
		{"negative loan credit", `UPDATE loans SET credit_balance = -1 WHERE id = ?`, []any{loan.ID}},
		{"zero payment amount", insertPayment, []any{f.companyID, f.userID, loan.ID, 0, 0}},
		{"negative credit after payment", insertPayment, []any{f.companyID, f.userID, loan.ID, 10, -1}},
		{"term above 120 months", insertLoan, []any{f.userID, f.companyID, 121}},
		{"term of zero months", insertLoan, []any{f.userID, f.companyID, 0}},
	}
	for _, tc := range bad {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			tx := f.db.Begin()
			defer tx.Rollback()
			if err := tx.Exec(tc.stmt, tc.args...).Error; err == nil {
				t.Error("expected the database to reject this")
			}
		})
	}
}

func TestPaymentRepo_GetByEventReturnsSettledInstallmentsAndCreditAfter(t *testing.T) {
	f := newFixture(t)
	loan, _ := f.createLoan(t, f.companyID, 400_000, 4, day(2026, 1, 15))

	over := f.pay(t, loan, 250_000) // settles #1 and #2, saves 50 000
	under := f.pay(t, loan, 30_000) // 80 000 available: settles nothing
	ctx := context.Background()

	got, err := f.payments.GetByEvent(ctx, f.companyID, over.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LoanID != loan.ID || got.UserID != f.userID || got.Amount != 250_000 || got.CreditBalanceAfter != 50_000 || !got.PaidAt.Equal(paidAt) {
		t.Errorf("payment fields wrong: %+v", got)
	}
	if !slices.Equal(numbersOf(got.Settled), []int{1, 2}) {
		t.Errorf("settled = %v, want [1 2]", numbersOf(got.Settled))
	}
	for _, inst := range got.Settled {
		if inst.Status != domain.StatusPaid || inst.PaymentEventID == nil || *inst.PaymentEventID != over.EventID {
			t.Errorf("settled installment #%d not linked to the payment: %+v", inst.Number, inst)
		}
	}

	got, err = f.payments.GetByEvent(ctx, f.companyID, under.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Settled) != 0 || got.CreditBalanceAfter != 80_000 {
		t.Errorf("underpayment: settled %d installments, credit after %d; want none and 80000", len(got.Settled), got.CreditBalanceAfter)
	}

	other := testutil.SeedCompany(t, f.db, "other-secret")
	for name, tc := range map[string]struct {
		company int64
		event   uuid.UUID
	}{
		"unknown event":                 {f.companyID, uuid.New()},
		"same event, different company": {other, over.EventID},
	} {
		if _, err := f.payments.GetByEvent(ctx, tc.company, tc.event); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s: got %v, want ErrNotFound", name, err)
		}
	}
	testutil.AssertLedger(t, f.db, loan.ID)
}

func TestPaymentRepo_EventIDIsUniquePerCompany(t *testing.T) {
	f := newFixture(t)
	otherCompany := testutil.SeedCompany(t, f.db, "other-secret")
	loan, _ := f.createLoan(t, f.companyID, 400_000, 4, day(2026, 1, 15))
	loanB, _ := f.createLoan(t, otherCompany, 400_000, 4, day(2026, 1, 15))
	original := f.pay(t, loan, 100_000)
	ctx := context.Background()

	resend := *original
	resend.Settled = nil
	if err := f.payments.Create(ctx, &resend); !errors.Is(err, domain.ErrEventReused) {
		t.Fatalf("same company and event: got %v, want ErrEventReused", err)
	}

	sameEventOtherCompany := &domain.Payment{
		CreditCompanyID: otherCompany, EventID: original.EventID, UserID: f.userID, LoanID: loanB.ID,
		Amount: 1, PaidAt: paidAt, CreditBalanceAfter: 1,
	}
	if err := f.payments.Create(ctx, sameEventOtherCompany); err != nil {
		t.Fatalf("the same event UUID under another company must be allowed: %v", err)
	}
}

func TestLoanRepo_GetForUpdateAndSaveCreditBalance(t *testing.T) {
	f := newFixture(t)
	otherCompany := testutil.SeedCompany(t, f.db, "other-secret")
	loan, _ := f.createLoan(t, f.companyID, 400_000, 4, day(2026, 1, 15))
	ctx := context.Background()

	if _, err := f.loans.GetForUpdate(ctx, loan.ID, f.companyID); err == nil || errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("outside a transaction: got %v, want a programming error about the missing transaction", err)
	}

	err := f.tx.WithinTx(ctx, func(ctx context.Context) error {
		got, err := f.loans.GetForUpdate(ctx, loan.ID, f.companyID)
		if err != nil {
			return err
		}
		if got.ID != loan.ID || got.UserID != f.userID || got.CreditCompanyID != f.companyID ||
			got.TotalAmount != 400_000 || got.TermMonths != 4 || got.CreditBalance != 0 ||
			!got.StartDate.Equal(day(2026, 1, 15)) {
			t.Errorf("loan read back wrong: %+v", got)
		}

		for name, tc := range map[string]struct{ id, company int64 }{
			"another company's loan": {loan.ID, otherCompany},
			"missing loan":           {999_999, f.companyID},
		} {
			if _, err := f.loans.GetForUpdate(ctx, tc.id, tc.company); !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("%s: got %v, want ErrNotFound", name, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, credit := range []int64{500, 0, 12_345} {
		loan.CreditBalance = credit
		if err := f.loans.SaveCreditBalance(ctx, loan); err != nil {
			t.Fatal(err)
		}
		if got := testutil.LoanCredit(t, f.db, loan.ID); got != credit {
			t.Errorf("credit = %d, want %d", got, credit)
		}
	}
	if err := f.loans.SaveCreditBalance(ctx, &domain.Loan{ID: 999_999}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing loan: got %v, want ErrNotFound", err)
	}
}

func TestInstallmentRepo_ListUnpaidIsOldestFirstAndSkipsPaidRows(t *testing.T) {
	f := newFixture(t)
	loan, _ := f.createLoan(t, f.companyID, 400_000, 4, day(2026, 1, 15))
	f.createLoan(t, f.companyID, 400_000, 4, day(2026, 1, 15)) // another loan's rows must not appear
	ctx := context.Background()

	got, err := f.installments.ListUnpaid(ctx, loan.ID)
	if err != nil || !slices.Equal(numbersOf(got), []int{1, 2, 3, 4}) {
		t.Fatalf("fresh loan: got %v (%v), want [1 2 3 4]", numbersOf(got), err)
	}

	f.pay(t, loan, 250_000) // settles #1 and #2
	got, err = f.installments.ListUnpaid(ctx, loan.ID)
	if err != nil || !slices.Equal(numbersOf(got), []int{3, 4}) {
		t.Fatalf("after a payment: got %v (%v), want [3 4]", numbersOf(got), err)
	}
	for _, inst := range got {
		if inst.LoanID != loan.ID || inst.Status != domain.StatusPending {
			t.Errorf("unexpected row: %+v", inst)
		}
	}

	f.pay(t, loan, 150_000) // settles #3 and #4
	if got, err = f.installments.ListUnpaid(ctx, loan.ID); err != nil || len(got) != 0 {
		t.Fatalf("fully paid loan: got %v (%v), want none", numbersOf(got), err)
	}
}

func TestInstallmentRepo_SaveSettled(t *testing.T) {
	f := newFixture(t)
	loan, _ := f.createLoan(t, f.companyID, 400_000, 4, day(2026, 1, 15))
	ctx := context.Background()

	if err := f.installments.SaveSettled(ctx, &domain.Payment{}); err != nil {
		t.Fatalf("a payment that settled nothing must be a no-op, got %v", err)
	}

	first := f.pay(t, loan, 100_000) // settles #1
	rows := testutil.LoanInstallments(t, f.db, loan.ID)
	second := f.insertPayment(t, f.companyID, loan.ID)

	// #1 is already paid, #2 is pending: the whole update must be refused and rolled back.
	stale := &domain.Payment{
		CreditCompanyID: f.companyID, EventID: second, PaidAt: paidAt,
		Settled: []domain.Installment{{ID: rows[0].ID}, {ID: rows[1].ID}},
	}
	err := f.tx.WithinTx(ctx, func(ctx context.Context) error { return f.installments.SaveSettled(ctx, stale) })
	if err == nil {
		t.Fatal("expected an error when one of the installments is not pending")
	}

	after := testutil.LoanInstallments(t, f.db, loan.ID)
	if after[1].Status != "pending" || after[1].PaymentEventID != nil {
		t.Errorf("#2 must stay untouched after the refused update: %+v", after[1])
	}
	if after[0].PaymentEventID == nil || *after[0].PaymentEventID != first.EventID {
		t.Errorf("#1 must stay linked to its original payment: %+v", after[0])
	}
}

func TestWithinTx_RollsBackEverythingWhenFnFails(t *testing.T) {
	f := newFixture(t)
	boom := errors.New("boom")

	err := f.tx.WithinTx(context.Background(), func(ctx context.Context) error {
		loan, err := domain.NewLoan(f.userID, f.companyID, 400_000, 4, day(2026, 1, 15))
		if err != nil {
			return err
		}
		if err := f.loans.Create(ctx, loan); err != nil {
			return err
		}
		if err := f.installments.CreateBatch(ctx, loan.Schedule()); err != nil {
			return err
		}
		unpaid, err := f.installments.ListUnpaid(ctx, loan.ID)
		if err != nil {
			return err
		}
		payment, err := loan.ApplyPayment(uuid.New(), 100_000, paidAt, unpaid)
		if err != nil {
			return err
		}
		if err := f.payments.Create(ctx, payment); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want boom", err)
	}
	for _, table := range []string{"loans", "installments", "payments"} {
		if n := testutil.CountRows(t, f.db, table); n != 0 {
			t.Errorf("%d rows survived the rollback in %s", n, table)
		}
	}
}

func TestCreditCompanyRepo_GetByID(t *testing.T) {
	f := newFixture(t)

	got, err := f.companies.GetByID(context.Background(), f.companyID)
	if err != nil || got.ID != f.companyID || got.WebhookSecret != "secret" {
		t.Fatalf("got (%+v, %v)", got, err)
	}
	if _, err := f.companies.GetByID(context.Background(), 999_999); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}
