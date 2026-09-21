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
	"interview/internal/interfaces"
	"interview/internal/repo"
	"interview/internal/service"
	"interview/internal/testutil"
)

const installment = 100_000 // every installment of the test loans (12 of them, 1_200_000 in total)

var paidAt = time.Date(2026, 2, 14, 10, 30, 0, 0, time.UTC)

type paymentEnv struct {
	svc       *service.PaymentService
	loans     *service.LoanService
	db        *gorm.DB
	userID    int64
	companyID int64
}

func newPaymentEnv(t *testing.T) paymentEnv {
	t.Helper()
	db := testutil.NewDB(t)
	tx := repo.NewTxManager(db)
	installments := repo.NewInstallmentRepo(db)
	loans := repo.NewLoanRepo(db)
	return paymentEnv{
		svc:       service.NewPaymentService(repo.NewCreditCompanyRepo(db), loans, installments, repo.NewPaymentRepo(db), tx),
		loans:     service.NewLoanService(loans, installments, tx),
		db:        db,
		userID:    testutil.SeedUser(t, db),
		companyID: testutil.SeedCompany(t, db, "secret"),
	}
}

func (e paymentEnv) newLoan(t *testing.T, companyID int64) int64 {
	t.Helper()
	loan, err := e.loans.CreateLoan(context.Background(), service.CreateLoanCommand{
		UserID: e.userID, CreditCompanyID: companyID, TotalAmount: 12 * installment, TermMonths: 12,
		StartDate: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("create loan: %v", err)
	}
	return loan.ID
}

func (e paymentEnv) command(loanID, amount int64, event uuid.UUID) interfaces.PaymentCommand {
	return interfaces.PaymentCommand{CompanyID: e.companyID, LoanID: loanID, EventID: event, Amount: amount, PaidAt: paidAt}
}

// pay sends a fresh payment event and fails the test on any error.
func (e paymentEnv) pay(t *testing.T, loanID, amount int64) interfaces.PaymentResult {
	t.Helper()
	res, err := e.svc.HandlePayment(context.Background(), e.command(loanID, amount, uuid.New()))
	if err != nil {
		t.Fatalf("pay %d: %v", amount, err)
	}
	if res.Duplicate {
		t.Fatalf("pay %d: a fresh event was reported as a duplicate", amount)
	}
	return res
}

func settledNumbers(res interfaces.PaymentResult) []int {
	var out []int
	for _, inst := range res.Payment.Settled {
		out = append(out, inst.Number)
	}
	return out
}

func TestHandlePayment_ExactAmountPaysOneInstallment(t *testing.T) {
	e := newPaymentEnv(t)
	loanID := e.newLoan(t, e.companyID)

	res := e.pay(t, loanID, installment)

	if !slices.Equal(settledNumbers(res), []int{1}) || res.Payment.CreditBalanceAfter != 0 {
		t.Fatalf("settled %v credit %d, want [1] and 0", settledNumbers(res), res.Payment.CreditBalanceAfter)
	}
	if got := testutil.PaidNumbers(t, e.db, loanID); !slices.Equal(got, []int{1}) {
		t.Fatalf("paid in DB = %v, want [1]", got)
	}
	testutil.AssertLedger(t, e.db, loanID)
}

func TestHandlePayment_OverpaymentPaysSeveralInstallmentsAndSavesTheRest(t *testing.T) {
	e := newPaymentEnv(t)
	loanID := e.newLoan(t, e.companyID)

	res := e.pay(t, loanID, 250_000)

	if !slices.Equal(settledNumbers(res), []int{1, 2}) || res.Payment.CreditBalanceAfter != 50_000 {
		t.Fatalf("settled %v credit %d, want [1 2] and 50000", settledNumbers(res), res.Payment.CreditBalanceAfter)
	}
	if got := testutil.PaidNumbers(t, e.db, loanID); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("paid in DB = %v, want [1 2]", got)
	}
	if got := testutil.LoanCredit(t, e.db, loanID); got != 50_000 {
		t.Fatalf("credit in DB = %d, want 50000", got)
	}
	testutil.AssertLedger(t, e.db, loanID)
}

func TestHandlePayment_ShortPaymentsAccumulateUntilTheyCoverAnInstallment(t *testing.T) {
	e := newPaymentEnv(t)
	loanID := e.newLoan(t, e.companyID)

	steps := []struct {
		wantPaid   []int
		wantCredit int64
	}{
		{nil, 40_000},
		{nil, 80_000},
		{[]int{1}, 20_000}, // 120 000 available: pays #1, keeps 20 000
	}
	for i, step := range steps {
		res := e.pay(t, loanID, 40_000)
		if !slices.Equal(settledNumbers(res), step.wantPaid) || res.Payment.CreditBalanceAfter != step.wantCredit {
			t.Fatalf("payment %d: settled %v credit %d, want %v and %d",
				i+1, settledNumbers(res), res.Payment.CreditBalanceAfter, step.wantPaid, step.wantCredit)
		}
		testutil.AssertLedger(t, e.db, loanID)
	}
}

func TestHandlePayment_SavedCreditIsSpentOnTheNextPayment(t *testing.T) {
	e := newPaymentEnv(t)
	loanID := e.newLoan(t, e.companyID)

	e.pay(t, loanID, 30_000)
	res := e.pay(t, loanID, 70_000)

	if !slices.Equal(settledNumbers(res), []int{1}) || res.Payment.CreditBalanceAfter != 0 {
		t.Fatalf("settled %v credit %d, want [1] and 0", settledNumbers(res), res.Payment.CreditBalanceAfter)
	}
	testutil.AssertLedger(t, e.db, loanID)
}

func TestHandlePayment_PaymentOnAFullyPaidLoanBecomesCredit(t *testing.T) {
	e := newPaymentEnv(t)
	loanID := e.newLoan(t, e.companyID)

	res := e.pay(t, loanID, 12*installment)
	if len(res.Payment.Settled) != 12 || res.Payment.CreditBalanceAfter != 0 {
		t.Fatalf("settled %d credit %d, want 12 and 0", len(res.Payment.Settled), res.Payment.CreditBalanceAfter)
	}

	res = e.pay(t, loanID, 5_000)
	if len(res.Payment.Settled) != 0 || res.Payment.CreditBalanceAfter != 5_000 {
		t.Fatalf("settled %d credit %d, want none and 5000", len(res.Payment.Settled), res.Payment.CreditBalanceAfter)
	}
	testutil.AssertLedger(t, e.db, loanID)
}

func TestHandlePayment_DuplicateReturnsTheOriginalResultAndChangesNothing(t *testing.T) {
	e := newPaymentEnv(t)
	loanID := e.newLoan(t, e.companyID)
	event := uuid.New()
	ctx := context.Background()

	first, err := e.svc.HandlePayment(ctx, e.command(loanID, 250_000, event))
	if err != nil || first.Duplicate {
		t.Fatalf("first delivery: duplicate=%v err=%v", first.Duplicate, err)
	}
	e.pay(t, loanID, 20_000) // moves the loan's credit on: 50 000 -> 70 000

	again, err := e.svc.HandlePayment(ctx, e.command(loanID, 250_000, event))
	if err != nil {
		t.Fatal(err)
	}
	if !again.Duplicate {
		t.Fatal("re-sent event was not reported as a duplicate")
	}
	if !slices.Equal(settledNumbers(again), []int{1, 2}) || again.Payment.CreditBalanceAfter != 50_000 {
		t.Fatalf("replay: settled %v credit %d; want the original answer [1 2] and 50000", settledNumbers(again), again.Payment.CreditBalanceAfter)
	}

	if n := testutil.CountRows(t, e.db, "payments"); n != 2 {
		t.Errorf("%d payment rows, want 2 (the replay must not add one)", n)
	}
	if got := testutil.LoanCredit(t, e.db, loanID); got != 70_000 {
		t.Errorf("credit = %d, want 70000 (the replay must not touch it)", got)
	}
	testutil.AssertLedger(t, e.db, loanID)
}

func TestHandlePayment_ReusingAnEventForADifferentPaymentIsRejected(t *testing.T) {
	e := newPaymentEnv(t)
	loanID, otherLoanID := e.newLoan(t, e.companyID), e.newLoan(t, e.companyID)
	event := uuid.New()
	ctx := context.Background()

	if _, err := e.svc.HandlePayment(ctx, e.command(loanID, installment, event)); err != nil {
		t.Fatal(err)
	}

	for name, cmd := range map[string]interfaces.PaymentCommand{
		"different amount": e.command(loanID, installment+1, event),
		"different loan":   e.command(otherLoanID, installment, event),
	} {
		if _, err := e.svc.HandlePayment(ctx, cmd); !errors.Is(err, domain.ErrEventReused) {
			t.Errorf("%s: got %v, want ErrEventReused", name, err)
		}
	}
	if got := testutil.PaidNumbers(t, e.db, otherLoanID); len(got) != 0 {
		t.Errorf("the other loan was modified: %v", got)
	}
	testutil.AssertLedger(t, e.db, loanID)
}

func TestHandlePayment_AnotherCompanysLoanIsNotFound(t *testing.T) {
	e := newPaymentEnv(t)
	otherCompany := testutil.SeedCompany(t, e.db, "other-secret")
	loanOfOther := e.newLoan(t, otherCompany)

	_, err := e.svc.HandlePayment(context.Background(), e.command(loanOfOther, installment, uuid.New()))
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	if n := testutil.CountRows(t, e.db, "payments"); n != 0 {
		t.Fatalf("%d payment rows written", n)
	}
}

func TestHandlePayment_InvalidAmountWritesNothing(t *testing.T) {
	e := newPaymentEnv(t)
	loanID := e.newLoan(t, e.companyID)

	for _, amount := range []int64{0, -5} {
		_, err := e.svc.HandlePayment(context.Background(), e.command(loanID, amount, uuid.New()))
		if !errors.Is(err, domain.ErrInvalidPayment) {
			t.Errorf("amount %d: got %v, want ErrInvalidPayment", amount, err)
		}
	}
	if n := testutil.CountRows(t, e.db, "payments"); n != 0 {
		t.Fatalf("%d payment rows written", n)
	}
}

func TestWebhookSecret(t *testing.T) {
	e := newPaymentEnv(t)

	secret, err := e.svc.WebhookSecret(context.Background(), e.companyID)
	if err != nil || secret != "secret" {
		t.Fatalf("got (%q, %v), want (secret, nil)", secret, err)
	}
	if _, err := e.svc.WebhookSecret(context.Background(), 999_999); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown company: got %v, want ErrNotFound", err)
	}
}
