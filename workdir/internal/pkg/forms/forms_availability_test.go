package forms

import (
	"testing"
	"time"
)

func TestAvailability(t *testing.T) {
	before, after := now.Add(-time.Hour), now.Add(time.Hour)
	tests := []struct {
		name         string
		form         Form
		tenantActive bool
		want         AvailabilityStatus
	}{
		{"active and unbounded", Form{IsActive: true}, true, Available},
		{"inside the window", Form{IsActive: true, StartDate: &before, EndDate: &after}, true, Available},
		{"starts now", Form{IsActive: true, StartDate: &now}, true, Available},
		{"inactive form", Form{IsActive: false}, true, Inactive},
		{"inactive tenant", Form{IsActive: true}, false, Inactive},
		{"inactive wins over the window", Form{IsActive: false, StartDate: &after}, true, Inactive},
		{"not started", Form{IsActive: true, StartDate: &after}, true, NotStarted},
		{"ended", Form{IsActive: true, EndDate: &before}, true, Ended},
		{"ends now", Form{IsActive: true, EndDate: &now}, true, Ended},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Availability(tt.form, tt.tenantActive, now); got != tt.want {
				t.Fatalf("Availability = %q, want %q", got, tt.want)
			}
		})
	}
}
