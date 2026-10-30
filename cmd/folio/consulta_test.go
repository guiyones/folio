package main

import (
	"strings"
	"testing"
	"time"

	"github.com/guiyones/folio/dfe"
)

func TestNextStep(t *testing.T) {
	now := time.Date(2026, 10, 30, 14, 20, 0, 0, time.Local)

	for name, tc := range map[string]struct {
		r    dfe.DistResult
		sent int64
		want string
	}{
		"137 that moved the NSU": {
			r:    dfe.DistResult{CStat: dfe.CStatNoDocs, UltNSU: 50, MaxNSU: 5499},
			want: "--nsu 50 a partir de 15:25",
		},
		"138 with more to fetch": {
			r:    dfe.DistResult{CStat: dfe.CStatDocsFound, UltNSU: 100, MaxNSU: 5499},
			want: "--nsu 100 agora",
		},
		"138 that reached the end": {
			r:    dfe.DistResult{CStat: dfe.CStatDocsFound, UltNSU: 5499, MaxNSU: 5499},
			want: "--nsu 5499 a partir de 15:25",
		},
		"656 keeps the NSU that was sent": {
			r:    dfe.DistResult{CStat: dfe.CStatOveruse, UltNSU: 0, MaxNSU: 0},
			sent: 50,
			want: "--nsu 50 a partir de 15:25",
		},
		"rejection keeps the NSU that was sent": {
			r:    dfe.DistResult{CStat: "215"},
			sent: 50,
			want: "--nsu 50",
		},
	} {
		if got := nextStep(tc.r, tc.sent, now); !strings.Contains(got, tc.want) {
			t.Errorf("%s: %q, want it to contain %q", name, got, tc.want)
		}
	}
}
