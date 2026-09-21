package repo

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type txKey struct{}

// Open connects to Postgres. GORM's own log lines (errors and slow queries) go to
// log; a nil log discards them. Query parameters are never logged, so amounts and
// identifiers stay out of the log files.
func Open(dsn string, log *slog.Logger) (*gorm.DB, error) {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return gorm.Open(postgres.Open(dsn), &gorm.Config{
		// Lets repos match unique violations with errors.Is(err, gorm.ErrDuplicatedKey).
		TranslateError: true,
		Logger: logger.New(gormWriter{log: log}, logger.Config{
			SlowThreshold:             500 * time.Millisecond,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
			ParameterizedQueries:      true,
		}),
	})
}

// gormWriter forwards GORM's formatted log lines to the application logger.
type gormWriter struct {
	log *slog.Logger
}

func (w gormWriter) Printf(format string, args ...any) {
	w.log.Warn("database", "detail", strings.TrimSpace(fmt.Sprintf(format, args...)))
}

// TxManager implements interfaces.TxManager. The transaction travels in the
// context, and every repo picks it up through conn.
type TxManager struct {
	db *gorm.DB
}

func NewTxManager(db *gorm.DB) *TxManager {
	return &TxManager{db: db}
}

func (m *TxManager) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
}

func txFrom(ctx context.Context) (*gorm.DB, bool) {
	tx, ok := ctx.Value(txKey{}).(*gorm.DB)
	return tx, ok
}

// conn returns the transaction carried by ctx, or the base DB if there is none.
func conn(ctx context.Context, db *gorm.DB) *gorm.DB {
	if tx, ok := txFrom(ctx); ok {
		return tx.WithContext(ctx)
	}
	return db.WithContext(ctx)
}
