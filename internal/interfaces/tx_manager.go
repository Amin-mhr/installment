package interfaces

import "context"

type TxManager interface {
	// WithinTx runs fn in a transaction; repositories called with the ctx passed
	// to fn take part in it. A non-nil error from fn rolls the transaction back.
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}
