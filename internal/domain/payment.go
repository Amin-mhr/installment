package domain

import (
	"time"

	"github.com/google/uuid"
)

// Payment is one accepted provider payment event. EventID is the provider's
// idempotency key, unique per credit company.
type Payment struct {
	CreditCompanyID    int64
	EventID            uuid.UUID
	UserID             int64
	LoanID             int64
	Amount             int64         // minor units
	PaidAt             time.Time     // as reported by the provider
	CreditBalanceAfter int64         // the loan's saved credit once this payment was applied
	Settled            []Installment // installments this payment paid off, oldest first
}

// Matches reports whether a delivery for (loanID, amount) is a re-send of this
// payment rather than a different payment reusing the same event ID.
func (p *Payment) Matches(loanID, amount int64) bool {
	return p.LoanID == loanID && p.Amount == amount
}
