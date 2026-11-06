package account_test

import (
	"testing"
	"time"

	"github.com/guiyones/folio/internal/account"
)

func TestStoredCertExpiry(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		notAfter     time.Time
		expired      bool
		expiringSoon bool
	}{
		{"far", now.AddDate(1, 0, 0), false, false},
		{"warn-edge", now.Add(account.CertWarn), false, true},
		{"soon", now.Add(24 * time.Hour), false, true},
		{"at-now", now, true, true},
		{"past", now.Add(-time.Hour), true, true},
	}
	for _, tt := range tests {
		sc := account.StoredCert{NotAfter: tt.notAfter}
		if got := sc.Expired(now); got != tt.expired {
			t.Errorf("%s: Expired = %v, want %v", tt.name, got, tt.expired)
		}
		if got := sc.ExpiringSoon(now); got != tt.expiringSoon {
			t.Errorf("%s: ExpiringSoon = %v, want %v", tt.name, got, tt.expiringSoon)
		}
	}
}
