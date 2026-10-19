package plans

import (
	"testing"
	"time"

	"app/internal/pkg/tenants"
)

func TestFor(t *testing.T) {
	tests := []struct {
		plan tenants.Plan
		want Limits
	}{
		{tenants.PlanFree, Limits{
			MaxForms:               3,
			MaxSubmissionsPerMonth: 100,
			MaxStorageBytes:        100 << 20,
			ShowBadge:              true,
		}},
		{tenants.PlanPro, Limits{
			MaxForms:               Unlimited,
			MaxSubmissionsPerMonth: 10_000,
			MaxStorageBytes:        10 << 30,
			Webhooks:               true,
			Exports:                true,
			EmailNotifications:     true,
		}},
		{"", For(tenants.PlanFree)},
		{"enterprise", For(tenants.PlanFree)},
	}
	for _, tt := range tests {
		t.Run(string(tt.plan), func(t *testing.T) {
			if got := For(tt.plan); got != tt.want {
				t.Fatalf("For(%q) = %+v, want %+v", tt.plan, got, tt.want)
			}
		})
	}
}

func TestFeatures(t *testing.T) {
	tests := []struct {
		plan                                tenants.Plan
		badge, webhooks, exports, emailNoti bool
	}{
		{tenants.PlanFree, true, false, false, false},
		{tenants.PlanPro, false, true, true, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.plan), func(t *testing.T) {
			l := For(tt.plan)
			if l.ShowBadge != tt.badge {
				t.Errorf("ShowBadge = %v, want %v", l.ShowBadge, tt.badge)
			}
			if l.Webhooks != tt.webhooks {
				t.Errorf("Webhooks = %v, want %v", l.Webhooks, tt.webhooks)
			}
			if l.Exports != tt.exports {
				t.Errorf("Exports = %v, want %v", l.Exports, tt.exports)
			}
			if l.EmailNotifications != tt.emailNoti {
				t.Errorf("EmailNotifications = %v, want %v", l.EmailNotifications, tt.emailNoti)
			}
		})
	}
}

func TestCanCreateForm(t *testing.T) {
	tests := []struct {
		name      string
		plan      tenants.Plan
		formCount int64
		want      bool
	}{
		{"free none", tenants.PlanFree, 0, true},
		{"free below", tenants.PlanFree, 2, true},
		{"free at limit", tenants.PlanFree, 3, false},
		{"free above", tenants.PlanFree, 4, false},
		{"pro none", tenants.PlanPro, 0, true},
		{"pro at free limit", tenants.PlanPro, 3, true},
		{"pro many", tenants.PlanPro, 1_000_000, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanCreateForm(For(tt.plan), tt.formCount); got != tt.want {
				t.Fatalf("CanCreateForm(%s, %d) = %v, want %v", tt.plan, tt.formCount, got, tt.want)
			}
		})
	}
}

func TestCanSubmit(t *testing.T) {
	tests := []struct {
		name       string
		plan       tenants.Plan
		monthCount int64
		want       bool
	}{
		{"free none", tenants.PlanFree, 0, true},
		{"free below", tenants.PlanFree, 99, true},
		{"free at limit", tenants.PlanFree, 100, false},
		{"free above", tenants.PlanFree, 101, false},
		{"pro none", tenants.PlanPro, 0, true},
		{"pro at free limit", tenants.PlanPro, 100, true},
		{"pro below", tenants.PlanPro, 9_999, true},
		{"pro at limit", tenants.PlanPro, 10_000, false},
		{"pro above", tenants.PlanPro, 10_001, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanSubmit(For(tt.plan), tt.monthCount); got != tt.want {
				t.Fatalf("CanSubmit(%s, %d) = %v, want %v", tt.plan, tt.monthCount, got, tt.want)
			}
		})
	}
}

func TestCanStore(t *testing.T) {
	const (
		freeMax int64 = 100 << 20
		proMax  int64 = 10 << 30
	)
	tests := []struct {
		name           string
		plan           tenants.Plan
		used, newBytes int64
		want           bool
	}{
		{"free empty", tenants.PlanFree, 0, 1, true},
		{"free below", tenants.PlanFree, freeMax - 2, 1, true},
		{"free lands at limit", tenants.PlanFree, freeMax - 1, 1, true},
		{"free one byte over", tenants.PlanFree, freeMax, 1, false},
		{"free single upload over", tenants.PlanFree, 0, freeMax + 1, false},
		{"free already above", tenants.PlanFree, freeMax + 1, 0, false},
		{"pro beyond free limit", tenants.PlanPro, freeMax, 1, true},
		{"pro lands at limit", tenants.PlanPro, proMax - 10, 10, true},
		{"pro one byte over", tenants.PlanPro, proMax - 10, 11, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanStore(For(tt.plan), tt.used, tt.newBytes); got != tt.want {
				t.Fatalf("CanStore(%s, %d, %d) = %v, want %v", tt.plan, tt.used, tt.newBytes, got, tt.want)
			}
		})
	}
}

func TestUnlimited(t *testing.T) {
	l := Limits{MaxForms: Unlimited, MaxSubmissionsPerMonth: Unlimited, MaxStorageBytes: Unlimited}
	if !CanCreateForm(l, 1<<40) || !CanSubmit(l, 1<<40) || !CanStore(l, 1<<40, 1<<40) {
		t.Fatal("Unlimited limits must allow everything")
	}
}

func TestMonthStart(t *testing.T) {
	brt := time.FixedZone("BRT", -3*3600)
	want := func(y int, m time.Month) time.Time { return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC) }
	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"mid month", time.Date(2026, 10, 19, 15, 4, 5, 6, time.UTC), want(2026, 10)},
		{"first instant", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), want(2026, 10)},
		{"last instant", time.Date(2026, 10, 31, 23, 59, 59, 999_999_999, time.UTC), want(2026, 10)},
		{"january", time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC), want(2027, 1)},
		{"non-UTC crosses month", time.Date(2026, 10, 31, 22, 0, 0, 0, brt), want(2026, 11)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MonthStart(tt.now)
			if !got.Equal(tt.want) || got.Location() != time.UTC {
				t.Fatalf("MonthStart(%v) = %v, want %v", tt.now, got, tt.want)
			}
		})
	}
}
