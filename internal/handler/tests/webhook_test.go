package tests

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"interview/internal/handler"
	"interview/internal/repo"
	"interview/internal/service"
	"interview/internal/testutil"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

const (
	secretA       = "secret-of-company-a"
	installment   = 100_000 // every installment of the test loans: 12 of them, 1_200_000 in total
	paidAtRFC3339 = "2026-02-14T10:30:00Z"
)

type env struct {
	db        *gorm.DB
	router    http.Handler
	loans     *service.LoanService
	userID    int64
	companyID int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db := testutil.NewDB(t)
	tx := repo.NewTxManager(db)
	installments := repo.NewInstallmentRepo(db)
	loans := repo.NewLoanRepo(db)
	payments := service.NewPaymentService(repo.NewCreditCompanyRepo(db), loans, installments, repo.NewPaymentRepo(db), tx)

	return &env{
		db:        db,
		router:    handler.NewRouter(payments, slog.New(slog.NewTextHandler(io.Discard, nil))),
		loans:     service.NewLoanService(loans, installments, tx),
		userID:    testutil.SeedUser(t, db),
		companyID: testutil.SeedCompany(t, db, secretA),
	}
}

// newLoan creates a 12-installment loan (100 000 each) with the given company.
func (e *env) newLoan(t *testing.T, companyID int64) int64 {
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

func paymentBody(event uuid.UUID, loanID, amount int64) []byte {
	return []byte(fmt.Sprintf(`{"event_id":%q,"loan_id":%d,"amount":%d,"paid_at":%q}`,
		event, loanID, amount, paidAtRFC3339))
}

// sign is written independently of the handler so the tests pin the documented scheme.
func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func webhookPath(companyID any) string {
	return fmt.Sprintf("/webhooks/credit-companies/%v/payments", companyID)
}

type reply struct {
	Status           string `json:"status"`
	InstallmentsPaid []struct {
		ID     int64 `json:"id"`
		Number int   `json:"number"`
		Amount int64 `json:"amount"`
	} `json:"installments_paid"`
	CreditBalance int64  `json:"credit_balance"`
	Error         string `json:"error"`
	raw           string
}

func (r reply) numbers() []int {
	var out []int
	for _, inst := range r.InstallmentsPaid {
		out = append(out, inst.Number)
	}
	return out
}

// send posts body with an explicit X-Signature header (omitted when empty).
func (e *env) send(companyID any, body []byte, signature string) (int, reply) {
	req := httptest.NewRequest(http.MethodPost, webhookPath(companyID), strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	if signature != "" {
		req.Header.Set("X-Signature", signature)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)

	var r reply
	_ = json.Unmarshal(rec.Body.Bytes(), &r)
	r.raw = rec.Body.String()
	return rec.Code, r
}

// deliver posts body correctly signed with secret.
func (e *env) deliver(companyID any, secret string, body []byte) (int, reply) {
	return e.send(companyID, body, sign(secret, body))
}

// pay delivers a fresh, correctly signed payment for the fixture's company.
func (e *env) pay(t *testing.T, loanID, amount int64) reply {
	t.Helper()
	code, r := e.deliver(e.companyID, secretA, paymentBody(uuid.New(), loanID, amount))
	if code != http.StatusOK {
		t.Fatalf("pay %d: status %d (%s)", amount, code, r.raw)
	}
	return r
}

func expect(t *testing.T, gotCode int, r reply, wantCode int) {
	t.Helper()
	if gotCode != wantCode {
		t.Fatalf("status = %d, want %d (body %s)", gotCode, wantCode, r.raw)
	}
}

// assertUntouched checks that no payment was recorded and nothing was paid or saved.
func assertUntouched(t *testing.T, e *env, loanID int64) {
	t.Helper()
	if n := testutil.CountRows(t, e.db, "payments"); n != 0 {
		t.Fatalf("%d payment rows exist, want none", n)
	}
	if paid := testutil.PaidNumbers(t, e.db, loanID); len(paid) != 0 {
		t.Fatalf("installments %v were paid, want none", paid)
	}
	if credit := testutil.LoanCredit(t, e.db, loanID); credit != 0 {
		t.Fatalf("credit = %d, want 0", credit)
	}
}

func TestWebhook_ExactPaymentPaysOneInstallment(t *testing.T) {
	e := newEnv(t)
	loanID := e.newLoan(t, e.companyID)

	code, r := e.deliver(e.companyID, secretA, paymentBody(uuid.New(), loanID, installment))
	expect(t, code, r, http.StatusOK)

	if r.Status != "applied" || !slices.Equal(r.numbers(), []int{1}) || r.CreditBalance != 0 {
		t.Fatalf("reply = %s, want applied, [1], credit 0", r.raw)
	}
	first := testutil.LoanInstallments(t, e.db, loanID)[0]
	wantPaidAt, _ := time.Parse(time.RFC3339, paidAtRFC3339)
	if first.Status != "paid" || first.PaidAt == nil || !first.PaidAt.Equal(wantPaidAt) || first.PaymentEventID == nil {
		t.Fatalf("installment #1 not settled as expected: %+v", first)
	}
	if r.InstallmentsPaid[0].ID != first.ID || r.InstallmentsPaid[0].Amount != installment {
		t.Fatalf("reply describes the wrong installment: %s", r.raw)
	}
	testutil.AssertLedger(t, e.db, loanID)
}

func TestWebhook_OverpaymentPaysSeveralInstallmentsAndSavesTheRest(t *testing.T) {
	e := newEnv(t)
	loanID := e.newLoan(t, e.companyID)

	code, r := e.deliver(e.companyID, secretA, paymentBody(uuid.New(), loanID, 250_000))
	expect(t, code, r, http.StatusOK)

	if r.Status != "applied" || !slices.Equal(r.numbers(), []int{1, 2}) || r.CreditBalance != 50_000 {
		t.Fatalf("reply = %s, want applied, [1 2], credit 50000", r.raw)
	}
	if got := testutil.PaidNumbers(t, e.db, loanID); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("paid in DB = %v, want [1 2]", got)
	}
	if got := testutil.LoanCredit(t, e.db, loanID); got != 50_000 {
		t.Fatalf("credit in DB = %d, want 50000", got)
	}
	testutil.AssertLedger(t, e.db, loanID)
}

func TestWebhook_ShortPaymentIsSavedThenCompletedByTheNextOne(t *testing.T) {
	e := newEnv(t)
	loanID := e.newLoan(t, e.companyID)

	short := e.pay(t, loanID, 30_000)
	if short.Status != "applied" || len(short.InstallmentsPaid) != 0 || short.CreditBalance != 30_000 {
		t.Fatalf("short payment reply = %s, want applied, no installments, credit 30000", short.raw)
	}
	if !strings.Contains(short.raw, `"installments_paid":[]`) {
		t.Fatalf("an underpayment must encode installments_paid as [], not null: %s", short.raw)
	}
	if paid := testutil.PaidNumbers(t, e.db, loanID); len(paid) != 0 {
		t.Fatalf("installments %v were paid by an underpayment", paid)
	}

	rest := e.pay(t, loanID, 70_000)
	if !slices.Equal(rest.numbers(), []int{1}) || rest.CreditBalance != 0 {
		t.Fatalf("second payment reply = %s, want [1] and credit 0", rest.raw)
	}
	testutil.AssertLedger(t, e.db, loanID)
}

func TestWebhook_RedeliveryReturnsTheOriginalAnswerAndChangesNothing(t *testing.T) {
	e := newEnv(t)
	loanID := e.newLoan(t, e.companyID)
	body := paymentBody(uuid.New(), loanID, 250_000)

	code, first := e.deliver(e.companyID, secretA, body)
	expect(t, code, first, http.StatusOK)
	e.pay(t, loanID, 20_000) // moves the loan's credit on: 50 000 -> 70 000

	for i := 0; i < 3; i++ {
		code, again := e.deliver(e.companyID, secretA, body)
		expect(t, code, again, http.StatusOK)
		if again.Status != "already_processed" {
			t.Fatalf("redelivery %d: status %q, want already_processed", i, again.Status)
		}
		if !reflect.DeepEqual(again.InstallmentsPaid, first.InstallmentsPaid) || again.CreditBalance != first.CreditBalance {
			t.Fatalf("redelivery %d answered %s, want the original %s", i, again.raw, first.raw)
		}
	}

	if n := testutil.CountRows(t, e.db, "payments"); n != 2 {
		t.Errorf("%d payment rows, want 2", n)
	}
	if got := testutil.LoanCredit(t, e.db, loanID); got != 70_000 {
		t.Errorf("credit = %d, want 70000", got)
	}
	testutil.AssertLedger(t, e.db, loanID)
}

func TestWebhook_ReusingAnEventIDForADifferentPaymentConflicts(t *testing.T) {
	e := newEnv(t)
	loanID, otherLoanID := e.newLoan(t, e.companyID), e.newLoan(t, e.companyID)
	event := uuid.New()

	code, r := e.deliver(e.companyID, secretA, paymentBody(event, loanID, installment))
	expect(t, code, r, http.StatusOK)

	code, r = e.deliver(e.companyID, secretA, paymentBody(event, loanID, installment+1))
	expect(t, code, r, http.StatusConflict)
	code, r = e.deliver(e.companyID, secretA, paymentBody(event, otherLoanID, installment))
	expect(t, code, r, http.StatusConflict)

	if got := testutil.PaidNumbers(t, e.db, otherLoanID); len(got) != 0 {
		t.Fatalf("the other loan was modified: %v", got)
	}
	testutil.AssertLedger(t, e.db, loanID)
}

func TestWebhook_RejectsBadAuthentication(t *testing.T) {
	e := newEnv(t)
	loanID := e.newLoan(t, e.companyID)
	body := paymentBody(uuid.New(), loanID, installment)
	valid := sign(secretA, body)

	tampered := paymentBody(uuid.New(), loanID, 1) // signed for `body`, sent as something else
	cases := []struct {
		name      string
		companyID any
		body      []byte
		signature string
	}{
		{"missing signature", e.companyID, body, ""},
		{"missing sha256= prefix", e.companyID, body, strings.TrimPrefix(valid, "sha256=")},
		{"wrong scheme", e.companyID, body, "md5=" + strings.TrimPrefix(valid, "sha256=")},
		{"non-hex signature", e.companyID, body, "sha256=not-hex"},
		{"signed with another secret", e.companyID, body, sign("some-other-secret", body)},
		{"body altered after signing", e.companyID, tampered, valid},
		{"unknown company", 999_999, body, valid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, r := e.send(tc.companyID, tc.body, tc.signature)
			expect(t, code, r, http.StatusUnauthorized)
			if r.Error != "invalid signature" {
				t.Errorf("error = %q, want %q", r.Error, "invalid signature")
			}
			assertUntouched(t, e, loanID)
		})
	}
}

func TestWebhook_CompanyCannotPayAnotherCompanysLoan(t *testing.T) {
	e := newEnv(t)
	companyB := testutil.SeedCompany(t, e.db, "secret-of-company-b")
	loanOfB := e.newLoan(t, companyB)

	// Company A authenticates correctly, but names a loan that belongs to B.
	code, r := e.deliver(e.companyID, secretA, paymentBody(uuid.New(), loanOfB, installment))
	expect(t, code, r, http.StatusNotFound)
	if r.Error != "loan not found" {
		t.Errorf("error = %q, want %q", r.Error, "loan not found")
	}
	assertUntouched(t, e, loanOfB)
}

func TestWebhook_UnknownLoanIsNotFound(t *testing.T) {
	e := newEnv(t)

	code, r := e.deliver(e.companyID, secretA, paymentBody(uuid.New(), 999_999, installment))
	expect(t, code, r, http.StatusNotFound)
}

func TestWebhook_RejectsInvalidRequests(t *testing.T) {
	e := newEnv(t)
	loanID := e.newLoan(t, e.companyID)
	event := uuid.New().String()

	cases := map[string]string{
		"not JSON":                  `this is not json`,
		"empty body":                ``,
		"JSON array, not an object": `[1,2,3]`,
		"missing event_id":          fmt.Sprintf(`{"loan_id":%d,"amount":%d,"paid_at":%q}`, loanID, installment, paidAtRFC3339),
		"nil event_id":              fmt.Sprintf(`{"event_id":%q,"loan_id":%d,"amount":%d,"paid_at":%q}`, uuid.Nil, loanID, installment, paidAtRFC3339),
		"event_id not a UUID":       fmt.Sprintf(`{"event_id":"tx-123","loan_id":%d,"amount":%d,"paid_at":%q}`, loanID, installment, paidAtRFC3339),
		"missing loan_id":           fmt.Sprintf(`{"event_id":%q,"amount":%d,"paid_at":%q}`, event, installment, paidAtRFC3339),
		"loan_id zero":              fmt.Sprintf(`{"event_id":%q,"loan_id":0,"amount":%d,"paid_at":%q}`, event, installment, paidAtRFC3339),
		"the old installment_id":    fmt.Sprintf(`{"event_id":%q,"installment_id":%d,"amount":%d,"paid_at":%q}`, event, 1, installment, paidAtRFC3339),
		"missing amount":            fmt.Sprintf(`{"event_id":%q,"loan_id":%d,"paid_at":%q}`, event, loanID, paidAtRFC3339),
		"amount zero":               fmt.Sprintf(`{"event_id":%q,"loan_id":%d,"amount":0,"paid_at":%q}`, event, loanID, paidAtRFC3339),
		"negative amount":           fmt.Sprintf(`{"event_id":%q,"loan_id":%d,"amount":-5,"paid_at":%q}`, event, loanID, paidAtRFC3339),
		"amount is a string":        fmt.Sprintf(`{"event_id":%q,"loan_id":%d,"amount":"100000","paid_at":%q}`, event, loanID, paidAtRFC3339),
		"amount beyond int64":       fmt.Sprintf(`{"event_id":%q,"loan_id":%d,"amount":99999999999999999999,"paid_at":%q}`, event, loanID, paidAtRFC3339),
		"missing paid_at":           fmt.Sprintf(`{"event_id":%q,"loan_id":%d,"amount":%d}`, event, loanID, installment),
		"paid_at not RFC 3339":      fmt.Sprintf(`{"event_id":%q,"loan_id":%d,"amount":%d,"paid_at":"yesterday"}`, event, loanID, installment),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			code, r := e.deliver(e.companyID, secretA, []byte(body))
			expect(t, code, r, http.StatusBadRequest)
			if r.Error == "" {
				t.Error("error body is empty")
			}
			assertUntouched(t, e, loanID)
		})
	}

	t.Run("non-numeric company id", func(t *testing.T) {
		code, r := e.deliver("abc", secretA, paymentBody(uuid.New(), loanID, installment))
		expect(t, code, r, http.StatusBadRequest)
		if r.Error != "invalid company id" {
			t.Errorf("error = %q, want %q", r.Error, "invalid company id")
		}
	})
}

func TestWebhook_IgnoresUnknownFields(t *testing.T) {
	e := newEnv(t)
	loanID := e.newLoan(t, e.companyID)
	body := []byte(fmt.Sprintf(`{"event_id":%q,"loan_id":%d,"amount":%d,"paid_at":%q,"bank_note":"extra","meta":{"a":1}}`,
		uuid.New(), loanID, installment, paidAtRFC3339))

	code, r := e.deliver(e.companyID, secretA, body)
	expect(t, code, r, http.StatusOK)
}

func TestWebhook_StoresProviderPaidAtAsUTC(t *testing.T) {
	e := newEnv(t)
	loanID := e.newLoan(t, e.companyID)
	body := []byte(fmt.Sprintf(`{"event_id":%q,"loan_id":%d,"amount":%d,"paid_at":"2026-02-14T13:30:00+03:30"}`,
		uuid.New(), loanID, installment))

	code, r := e.deliver(e.companyID, secretA, body)
	expect(t, code, r, http.StatusOK)

	got := testutil.LoanInstallments(t, e.db, loanID)[0].PaidAt
	if want := time.Date(2026, 2, 14, 10, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("paid_at = %v, want %v", got, want)
	}
}

func TestWebhook_BodyOver64KiBIsRejected(t *testing.T) {
	e := newEnv(t)
	loanID := e.newLoan(t, e.companyID)
	body := []byte(fmt.Sprintf(`{"event_id":%q,"loan_id":%d,"amount":%d,"paid_at":%q,"padding":%q}`,
		uuid.New(), loanID, installment, paidAtRFC3339, strings.Repeat("a", 70<<10)))

	code, r := e.deliver(e.companyID, secretA, body)
	expect(t, code, r, http.StatusRequestEntityTooLarge)
	assertUntouched(t, e, loanID)
}

func TestWebhook_OnlyPostIsAllowedAndTheOldRouteIsGone(t *testing.T) {
	e := newEnv(t)

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		e.router.ServeHTTP(rec, httptest.NewRequest(method, webhookPath(e.companyID), nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", method, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	oldPath := fmt.Sprintf("/webhooks/credit-companies/%d/installments/paid", e.companyID)
	e.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, oldPath, nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("old route: status = %d, want 404", rec.Code)
	}
}

func TestWebhook_ConcurrentDuplicateDeliveriesApplyExactlyOnce(t *testing.T) {
	e := newEnv(t)
	loanID := e.newLoan(t, e.companyID)
	body := paymentBody(uuid.New(), loanID, 250_000)

	const deliveries = 20
	codes := make([]int, deliveries)
	replies := make([]reply, deliveries)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < deliveries; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			codes[i], replies[i] = e.deliver(e.companyID, secretA, body)
		}(i)
	}
	close(start)
	wg.Wait()

	applied, processed := 0, 0
	for i := range codes {
		if codes[i] != http.StatusOK {
			t.Fatalf("delivery %d: status %d (%s)", i, codes[i], replies[i].raw)
		}
		if !slices.Equal(replies[i].numbers(), []int{1, 2}) || replies[i].CreditBalance != 50_000 {
			t.Fatalf("delivery %d answered %s, want [1 2] and credit 50000", i, replies[i].raw)
		}
		switch replies[i].Status {
		case "applied":
			applied++
		case "already_processed":
			processed++
		}
	}
	if applied != 1 || processed != deliveries-1 {
		t.Fatalf("applied=%d already_processed=%d, want 1 and %d", applied, processed, deliveries-1)
	}
	if n := testutil.CountRows(t, e.db, "payments"); n != 1 {
		t.Fatalf("%d payment rows, want 1", n)
	}
	if got := testutil.LoanCredit(t, e.db, loanID); got != 50_000 {
		t.Fatalf("credit = %d, want 50000 (applied once)", got)
	}
	testutil.AssertLedger(t, e.db, loanID)
}

// Ten different payments race on one loan. Because every payment takes the loan's
// row lock, none of them can overwrite another's read-modify-write of the credit.
func TestWebhook_ConcurrentDifferentPaymentsLoseNoMoney(t *testing.T) {
	e := newEnv(t)
	loanID := e.newLoan(t, e.companyID)

	const payments = 10
	const each = installment / 2 // half an installment: ten of them pay exactly five
	codes := make([]int, payments)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < payments; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			codes[i], _ = e.deliver(e.companyID, secretA, paymentBody(uuid.New(), loanID, each))
		}(i)
	}
	close(start)
	wg.Wait()

	for i, code := range codes {
		if code != http.StatusOK {
			t.Fatalf("payment %d: status %d", i, code)
		}
	}
	if got := testutil.PaidNumbers(t, e.db, loanID); !slices.Equal(got, []int{1, 2, 3, 4, 5}) {
		t.Fatalf("paid installments = %v, want [1 2 3 4 5]", got)
	}
	if got := testutil.LoanCredit(t, e.db, loanID); got != 0 {
		t.Fatalf("credit = %d, want 0", got)
	}
	if n := testutil.CountRows(t, e.db, "payments"); n != payments {
		t.Fatalf("%d payment rows, want %d", n, payments)
	}
	testutil.AssertLedger(t, e.db, loanID)
}
