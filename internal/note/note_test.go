package note_test

import (
	"errors"
	"testing"

	"github.com/guiyones/folio/internal/note"
)

func TestCanRequestCiencia(t *testing.T) {
	tests := []struct {
		name    string
		ciencia note.Ciencia
		auto    bool
		want    error
	}{
		{"failed", note.CienciaFailed, true, nil},
		{"not-applicable-manual", note.CienciaNotApplicable, false, nil},
		{"not-applicable-auto", note.CienciaNotApplicable, true, note.ErrAutoCiencia},
		{"pending", note.CienciaPending, false, note.ErrCienciaState},
		{"done", note.CienciaDone, true, note.ErrCienciaState},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := note.CanRequestCiencia(note.Note{Ciencia: tt.ciencia}, tt.auto)
			if !errors.Is(err, tt.want) || (tt.want == nil && err != nil) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}

	err := note.CanRequestCiencia(note.Note{Ciencia: note.CienciaDone}, false)
	if err == nil || err.Error() != "a Ciência desta nota já está feita" {
		t.Errorf("message = %v", err)
	}
}
