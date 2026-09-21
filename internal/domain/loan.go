package domain

import (
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
)

// MaxTermMonths caps the loan term so a typo cannot generate a huge schedule.
const MaxTermMonths = 120

type Loan struct {
	ID              int64
	UserID          int64
	CreditCompanyID int64
	TotalAmount     int64     // total repayable, minor units
	TermMonths      int       // number of monthly installments
	StartDate       time.Time // calendar date, UTC midnight
	CreditBalance   int64     // money received but not yet applied to an installment
}

func NewLoan(userID, companyID, totalAmount int64, termMonths int, startDate time.Time) (*Loan, error) {
	switch {
	case termMonths < 1 || termMonths > MaxTermMonths:
		return nil, fmt.Errorf("%w: term must be between 1 and %d months, got %d", ErrInvalidLoan, MaxTermMonths, termMonths)
	case totalAmount < int64(termMonths):
		return nil, fmt.Errorf("%w: total amount %d cannot be split into %d installments", ErrInvalidLoan, totalAmount, termMonths)
	}

	y, m, d := startDate.Date()
	return &Loan{
		UserID:          userID,
		CreditCompanyID: companyID,
		TotalAmount:     totalAmount,
		TermMonths:      termMonths,
		StartDate:       time.Date(y, m, d, 0, 0, 0, 0, time.UTC),
	}, nil
}

// Schedule returns one pending installment per month of the term. Call it after
// the loan has been persisted so the installments carry its ID. Any remainder from
// splitting the total evenly goes on the last installment.
func (l *Loan) Schedule() []Installment {
	term := int64(l.TermMonths)
	base, remainder := l.TotalAmount/term, l.TotalAmount%term

	installments := make([]Installment, l.TermMonths)
	for n := 1; n <= l.TermMonths; n++ {
		amount := base
		if n == l.TermMonths {
			amount += remainder
		}
		installments[n-1] = Installment{
			LoanID:          l.ID,
			UserID:          l.UserID,
			CreditCompanyID: l.CreditCompanyID,
			Number:          n,
			Amount:          amount,
			DueDate:         addMonths(l.StartDate, n),
			Status:          StatusPending,
		}
	}
	return installments
}

// ApplyPayment spends the loan's saved credit plus the new payment on unpaid
// installments, oldest first, paying each in full. Whatever cannot cover the next
// installment becomes the loan's new credit, so a payment smaller than an
// installment is simply saved until it adds up. unpaid must be this loan's pending
// installments in ascending installment number; it is not modified.
func (l *Loan) ApplyPayment(eventID uuid.UUID, amount int64, paidAt time.Time, unpaid []Installment) (*Payment, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("%w: amount must be positive, got %d", ErrInvalidPayment, amount)
	}
	if amount > math.MaxInt64-l.CreditBalance {
		return nil, fmt.Errorf("%w: amount %d is too large", ErrInvalidPayment, amount)
	}

	paidAt = paidAt.UTC()
	available := l.CreditBalance + amount

	var settled []Installment
	for _, inst := range unpaid {
		if available < inst.Amount {
			break
		}
		available -= inst.Amount
		inst.settle(eventID, paidAt)
		settled = append(settled, inst)
	}

	l.CreditBalance = available
	return &Payment{
		CreditCompanyID:    l.CreditCompanyID,
		EventID:            eventID,
		UserID:             l.UserID,
		LoanID:             l.ID,
		Amount:             amount,
		PaidAt:             paidAt,
		CreditBalanceAfter: available,
		Settled:            settled,
	}, nil
}

// addMonths adds n months to a calendar date, clamping to the end of the target
// month (Jan 31 + 1 month = Feb 28/29). Always computed from the original start
// date, so clamping never accumulates (Jan 31 + 2 months = Mar 31).
func addMonths(start time.Time, n int) time.Time {
	y, m, d := start.Date()
	firstOfTarget := time.Date(y, m+time.Month(n), 1, 0, 0, 0, 0, time.UTC)
	lastDay := firstOfTarget.AddDate(0, 1, -1).Day()
	if d > lastDay {
		d = lastDay
	}
	return time.Date(firstOfTarget.Year(), firstOfTarget.Month(), d, 0, 0, 0, 0, time.UTC)
}
