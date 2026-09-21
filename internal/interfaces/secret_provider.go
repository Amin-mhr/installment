package interfaces

import "context"

// SecretProvider looks up the key a credit company signs its webhooks with.
type SecretProvider interface {
	// WebhookSecret returns domain.ErrNotFound if there is no such company.
	WebhookSecret(ctx context.Context, companyID int64) (string, error)
}
