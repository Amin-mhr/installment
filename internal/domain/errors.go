package domain

import "errors"

var (
	ErrNotFound       = errors.New("not found")
	ErrInvalidLoan    = errors.New("invalid loan")
	ErrInvalidPayment = errors.New("invalid payment")
	ErrEventReused    = errors.New("event id already used for a different payment")
)
