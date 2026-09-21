package tests

import (
	"errors"
	"testing"
	"time"

	"interview/internal/domain"
)

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// mustLoan builds a loan for user 7 with credit company 3 and ID 99.
func mustLoan(t *testing.T, total int64, term int, start time.Time) *domain.Loan {
	t.Helper()
	loan, err := domain.NewLoan(7, 3, total, term, start)
	if err != nil {
		t.Fatalf("NewLoan: %v", err)
	}
	loan.ID = 99
	return loan
}

func TestNewLoan_Validation(t *testing.T) {
	tests := []struct {
		name    string
		total   int64
		term    int
		wantErr bool
	}{
		{"1 month", 1000, 1, false},
		{"4 months", 1000, 4, false},
		{"12 months", 1200, 12, false},
		{"18 months", 1800, 18, false},
		{"max term", 12000, domain.MaxTermMonths, false},
		{"total equals term", 12, 12, false},
		{"term zero", 1000, 0, true},
		{"term negative", 1000, -1, true},
		{"term above max", 100000, domain.MaxTermMonths + 1, true},
		{"total below term", 11, 12, true},
		{"total zero", 0, 12, true},
		{"total negative", -5, 12, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := domain.NewLoan(1, 1, tc.total, tc.term, date(2026, 1, 15))
			if tc.wantErr {
				if !errors.Is(err, domain.ErrInvalidLoan) {
					t.Fatalf("want ErrInvalidLoan, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestNewLoan_NormalizesStartDateToUTCMidnight(t *testing.T) {
	tehran := time.FixedZone("+0330", 3*3600+1800)
	start := time.Date(2026, 3, 10, 23, 45, 0, 0, tehran)

	loan, err := domain.NewLoan(1, 1, 1000, 4, start)
	if err != nil {
		t.Fatal(err)
	}
	if want := date(2026, 3, 10); !loan.StartDate.Equal(want) || loan.StartDate.Location() != time.UTC {
		t.Fatalf("StartDate = %v, want %v", loan.StartDate, want)
	}
}

func TestSchedule_ProducesOneInstallmentPerMonthOfTerm(t *testing.T) {
	for _, term := range []int{1, 4, 12, 18, domain.MaxTermMonths} {
		loan := mustLoan(t, int64(term)*1000, term, date(2026, 1, 15))
		got := loan.Schedule()

		if len(got) != term {
			t.Fatalf("term %d: got %d installments", term, len(got))
		}
		for i, inst := range got {
			if inst.Number != i+1 {
				t.Fatalf("term %d: installment %d has number %d", term, i, inst.Number)
			}
			if inst.Status != domain.StatusPending || inst.PaidAt != nil || inst.PaymentEventID != nil {
				t.Fatalf("term %d: installment %d is not a clean pending row: %+v", term, inst.Number, inst)
			}
			if inst.LoanID != 99 || inst.UserID != 7 || inst.CreditCompanyID != 3 {
				t.Fatalf("term %d: installment %d has wrong ownership: %+v", term, inst.Number, inst)
			}
		}
	}
}

func TestSchedule_AmountsSumToTotalWithRemainderOnLast(t *testing.T) {
	tests := []struct {
		total, wantBase, wantLast int64
		term                      int
	}{
		{total: 1200, term: 12, wantBase: 100, wantLast: 100}, // divides evenly
		{total: 1000, term: 12, wantBase: 83, wantLast: 87},   // remainder 4
		{total: 1000, term: 3, wantBase: 333, wantLast: 334},  // remainder 1
		{total: 500, term: 1, wantBase: 500, wantLast: 500},
		{total: 12, term: 12, wantBase: 1, wantLast: 1},
	}
	for _, tc := range tests {
		got := mustLoan(t, tc.total, tc.term, date(2026, 1, 15)).Schedule()

		var sum int64
		for i, inst := range got {
			sum += inst.Amount
			want := tc.wantBase
			if i == len(got)-1 {
				want = tc.wantLast
			}
			if inst.Amount != want {
				t.Errorf("total %d term %d: installment %d amount = %d, want %d", tc.total, tc.term, inst.Number, inst.Amount, want)
			}
		}
		if sum != tc.total {
			t.Errorf("total %d term %d: amounts sum to %d", tc.total, tc.term, sum)
		}
	}
}

func TestSchedule_DueDates(t *testing.T) {
	tests := []struct {
		name  string
		start time.Time
		term  int
		want  []time.Time
	}{
		{
			name: "same day each month", start: date(2026, 1, 15), term: 3,
			want: []time.Time{date(2026, 2, 15), date(2026, 3, 15), date(2026, 4, 15)},
		},
		{
			name: "month-end clamps and recovers, no drift", start: date(2026, 1, 31), term: 4,
			want: []time.Time{date(2026, 2, 28), date(2026, 3, 31), date(2026, 4, 30), date(2026, 5, 31)},
		},
		{
			name: "leap year February", start: date(2024, 1, 31), term: 2,
			want: []time.Time{date(2024, 2, 29), date(2024, 3, 31)},
		},
		{
			name: "crosses year boundary", start: date(2026, 11, 30), term: 4,
			want: []time.Time{date(2026, 12, 30), date(2027, 1, 30), date(2027, 2, 28), date(2027, 3, 30)},
		},
		{
			name: "december start", start: date(2026, 12, 31), term: 2,
			want: []time.Time{date(2027, 1, 31), date(2027, 2, 28)},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mustLoan(t, int64(tc.term)*1000, tc.term, tc.start).Schedule()
			for i, want := range tc.want {
				if !got[i].DueDate.Equal(want) {
					t.Errorf("installment %d due %s, want %s", got[i].Number, got[i].DueDate.Format("2006-01-02"), want.Format("2006-01-02"))
				}
			}
		})
	}
}

func TestSchedule_LastDueDateIsTermMonthsAfterStart(t *testing.T) {
	got := mustLoan(t, 18000, 18, date(2026, 3, 10)).Schedule()
	if want := date(2027, 9, 10); !got[17].DueDate.Equal(want) {
		t.Fatalf("last due date = %v, want %v", got[17].DueDate, want)
	}
}
