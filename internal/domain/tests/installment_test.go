package tests

import (
	"testing"
	"time"

	"interview/internal/domain"
)

func TestIsOverdue(t *testing.T) {
	now := time.Date(2026, 3, 1, 15, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		status domain.InstallmentStatus
		due    time.Time
		want   bool
	}{
		{"pending, due yesterday", domain.StatusPending, date(2026, 2, 28), true},
		{"pending, due today", domain.StatusPending, date(2026, 3, 1), false},
		{"pending, due tomorrow", domain.StatusPending, date(2026, 3, 2), false},
		{"paid, was due last month", domain.StatusPaid, date(2026, 2, 1), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inst := &domain.Installment{Status: tc.status, DueDate: tc.due}
			if got := inst.IsOverdue(now); got != tc.want {
				t.Fatalf("IsOverdue = %v, want %v", got, tc.want)
			}
		})
	}
}
