package tests

import (
	"errors"
	"math"
	"math/rand"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"interview/internal/domain"
)

var paidAt = time.Date(2026, 2, 14, 10, 0, 0, 0, time.UTC)

// fourBy100k is a loan of 4 installments of 100_000 each (loan 99, user 7, company 3).
func fourBy100k(t *testing.T) (*domain.Loan, []domain.Installment) {
	t.Helper()
	loan := mustLoan(t, 400_000, 4, date(2026, 1, 15))
	return loan, loan.Schedule()
}

func numbers(installments []domain.Installment) []int {
	var out []int
	for _, inst := range installments {
		out = append(out, inst.Number)
	}
	return out
}

func TestApplyPayment_AllocatesOldestFirstAndSavesTheRest(t *testing.T) {
	tests := []struct {
		name       string
		credit     int64 // already saved on the loan
		amount     int64
		wantPaid   []int
		wantCredit int64
	}{
		{"exact amount pays one", 0, 100_000, []int{1}, 0},
		{"double pays two", 0, 200_000, []int{1, 2}, 0},
		{"2.5x pays two, saves the half", 0, 250_000, []int{1, 2}, 50_000},
		{"less than an installment is saved", 0, 40_000, nil, 40_000},
		{"saved credit + payment completes an installment", 40_000, 70_000, []int{1}, 10_000},
		{"saved credit + payment still short", 30_000, 40_000, nil, 70_000},
		{"exactly the whole loan", 0, 400_000, []int{1, 2, 3, 4}, 0},
		{"more than the whole loan saves the excess", 0, 550_000, []int{1, 2, 3, 4}, 150_000},
		{"credit one short of an installment is completed by a tiny payment", 99_999, 1, []int{1}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			loan, unpaid := fourBy100k(t)
			loan.CreditBalance = tc.credit

			p, err := loan.ApplyPayment(uuid.New(), tc.amount, paidAt, unpaid)
			if err != nil {
				t.Fatal(err)
			}
			if got := numbers(p.Settled); !slices.Equal(got, tc.wantPaid) {
				t.Errorf("paid installments = %v, want %v", got, tc.wantPaid)
			}
			if loan.CreditBalance != tc.wantCredit || p.CreditBalanceAfter != tc.wantCredit {
				t.Errorf("credit = %d (payment says %d), want %d", loan.CreditBalance, p.CreditBalanceAfter, tc.wantCredit)
			}
		})
	}
}

func TestApplyPayment_NothingLeftToPayEverythingBecomesCredit(t *testing.T) {
	loan, _ := fourBy100k(t)
	loan.CreditBalance = 5

	p, err := loan.ApplyPayment(uuid.New(), 100, paidAt, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Settled) != 0 || loan.CreditBalance != 105 {
		t.Fatalf("settled %d installments, credit %d; want none and 105", len(p.Settled), loan.CreditBalance)
	}
}

func TestApplyPayment_StartsFromTheOldestUnpaidInstallment(t *testing.T) {
	loan, all := fourBy100k(t)

	p, err := loan.ApplyPayment(uuid.New(), 250_000, paidAt, all[2:]) // #1 and #2 are already paid
	if err != nil {
		t.Fatal(err)
	}
	if got := numbers(p.Settled); !slices.Equal(got, []int{3, 4}) {
		t.Fatalf("paid %v, want [3 4]", got)
	}
	if loan.CreditBalance != 50_000 {
		t.Fatalf("credit = %d, want 50000", loan.CreditBalance)
	}
}

func TestApplyPayment_UnevenLastInstallment(t *testing.T) {
	loan := mustLoan(t, 1000, 3, date(2026, 1, 15)) // 333, 333, 334
	unpaid := loan.Schedule()

	p, err := loan.ApplyPayment(uuid.New(), 700, paidAt, unpaid)
	if err != nil {
		t.Fatal(err)
	}
	if got := numbers(p.Settled); !slices.Equal(got, []int{1, 2}) || loan.CreditBalance != 34 {
		t.Fatalf("paid %v credit %d, want [1 2] and 34 (the last installment needs 334)", got, loan.CreditBalance)
	}

	p, err = loan.ApplyPayment(uuid.New(), 300, paidAt, unpaid[2:])
	if err != nil {
		t.Fatal(err)
	}
	if got := numbers(p.Settled); !slices.Equal(got, []int{3}) || loan.CreditBalance != 0 {
		t.Fatalf("paid %v credit %d, want [3] and 0", got, loan.CreditBalance)
	}
}

func TestApplyPayment_BuildsThePaymentRecord(t *testing.T) {
	loan, unpaid := fourBy100k(t)
	event := uuid.New()
	tehran := time.FixedZone("+0330", 3*3600+1800)

	p, err := loan.ApplyPayment(event, 250_000, time.Date(2026, 2, 14, 13, 30, 0, 0, tehran), unpaid)
	if err != nil {
		t.Fatal(err)
	}

	if p.CreditCompanyID != 3 || p.UserID != 7 || p.LoanID != 99 || p.EventID != event || p.Amount != 250_000 {
		t.Errorf("payment identity wrong: %+v", p)
	}
	if want := time.Date(2026, 2, 14, 10, 0, 0, 0, time.UTC); !p.PaidAt.Equal(want) || p.PaidAt.Location() != time.UTC {
		t.Errorf("PaidAt = %v, want %v in UTC", p.PaidAt, want)
	}
	for _, inst := range p.Settled {
		if inst.Status != domain.StatusPaid || inst.PaidAt == nil || !inst.PaidAt.Equal(p.PaidAt) ||
			inst.PaymentEventID == nil || *inst.PaymentEventID != event {
			t.Errorf("settled installment #%d not marked paid by this payment: %+v", inst.Number, inst)
		}
		if inst.Amount != 100_000 || inst.LoanID != 99 {
			t.Errorf("settled installment #%d lost its data: %+v", inst.Number, inst)
		}
	}
	for _, inst := range unpaid {
		if inst.Status != domain.StatusPending || inst.PaidAt != nil || inst.PaymentEventID != nil {
			t.Errorf("the caller's slice was modified: %+v", inst)
		}
	}
}

func TestApplyPayment_RejectsInvalidAmountsWithoutChangingTheLoan(t *testing.T) {
	loan, unpaid := fourBy100k(t)
	loan.CreditBalance = math.MaxInt64 - 10

	for name, amount := range map[string]int64{
		"zero":             0,
		"negative":         -1,
		"min int64":        math.MinInt64,
		"overflows credit": 11,
	} {
		if _, err := loan.ApplyPayment(uuid.New(), amount, paidAt, unpaid); !errors.Is(err, domain.ErrInvalidPayment) {
			t.Errorf("%s: got %v, want ErrInvalidPayment", name, err)
		}
		if loan.CreditBalance != math.MaxInt64-10 {
			t.Fatalf("%s: credit changed to %d", name, loan.CreditBalance)
		}
	}

	if _, err := loan.ApplyPayment(uuid.New(), 10, paidAt, unpaid); err != nil {
		t.Errorf("an amount that exactly reaches MaxInt64 is valid, got %v", err)
	}
}

func TestPaymentMatches(t *testing.T) {
	p := &domain.Payment{LoanID: 5, Amount: 1000}

	if !p.Matches(5, 1000) {
		t.Error("same loan and amount should match")
	}
	if p.Matches(6, 1000) || p.Matches(5, 999) {
		t.Error("a different loan or amount must not match")
	}
}

// Whatever sequence of payments arrives, money is neither created nor lost,
// installments settle strictly oldest-first, and the saved credit never grows
// large enough to pay the next installment.
func TestApplyPayment_RandomSequenceConservesMoney(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	loan := mustLoan(t, 1_000_003, 12, date(2026, 1, 15))
	unpaid := loan.Schedule()
	var received, applied int64

	for i := 0; i < 1000; i++ {
		amount := rng.Int63n(5000) + 1
		p, err := loan.ApplyPayment(uuid.New(), amount, paidAt, unpaid)
		if err != nil {
			t.Fatalf("payment %d: %v", i, err)
		}

		received += amount
		for j, inst := range p.Settled {
			if inst.Number != unpaid[j].Number {
				t.Fatalf("payment %d settled #%d, but the oldest unpaid was #%d", i, inst.Number, unpaid[j].Number)
			}
			applied += inst.Amount
		}
		unpaid = unpaid[len(p.Settled):]

		if received != applied+loan.CreditBalance {
			t.Fatalf("payment %d: received %d != applied %d + credit %d", i, received, applied, loan.CreditBalance)
		}
		if loan.CreditBalance < 0 {
			t.Fatalf("payment %d: negative credit %d", i, loan.CreditBalance)
		}
		if len(unpaid) > 0 && loan.CreditBalance >= unpaid[0].Amount {
			t.Fatalf("payment %d: credit %d could already pay installment #%d (%d)", i, loan.CreditBalance, unpaid[0].Number, unpaid[0].Amount)
		}
	}
	if len(unpaid) != 0 {
		t.Fatalf("%d installments unpaid after receiving %d; the loop should have paid the whole loan", len(unpaid), received)
	}
}
