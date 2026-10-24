package httpapi

import (
	"bytes"
	"context"
	"errors"

	"github.com/guiyones/folio/internal/httpapi/gen"
	"github.com/guiyones/folio/internal/note"
	"github.com/guiyones/folio/internal/platform/db"
	"github.com/jmoiron/sqlx"
)

// defaultPage is the page size when the client sends no limit.
const defaultPage = 100

func (s *server) ListNotes(ctx context.Context, req gen.ListNotesRequestObject) (gen.ListNotesResponseObject, error) {
	a, found, err := s.ownAccount(ctx, req.Id)
	if err != nil || !found {
		return gen.ListNotes404JSONResponse{NotFoundJSONResponse: notFound("conta")}, err
	}

	after := int64(0)
	if req.Params.After != nil {
		after = *req.Params.After
	}
	limit := defaultPage
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}

	notes, err := s.Notes.NotesAfter(ctx, a.ID, after, limit)
	if err != nil {
		return nil, err
	}

	out := gen.NoteList{Notes: make([]gen.Note, 0, len(notes)), NextAfter: after}
	for _, n := range notes {
		out.Notes = append(out.Notes, noteView(n))
		out.NextAfter = n.Seq
	}
	return gen.ListNotes200JSONResponse(out), nil
}

func (s *server) GetNoteXML(ctx context.Context, req gen.GetNoteXMLRequestObject) (gen.GetNoteXMLResponseObject, error) {
	n, found, err := s.ownNote(ctx, req.Id)
	if err != nil || !found {
		return gen.GetNoteXML404JSONResponse{NotFoundJSONResponse: notFound("nota")}, err
	}

	xml, kind, err := s.Notes.XML(ctx, n.ID)
	if errors.Is(err, note.ErrNoXML) {
		return gen.GetNoteXML404JSONResponse{NotFoundJSONResponse: gen.NotFoundJSONResponse(errorBody(note.ErrNoXML.Error()))}, nil
	}
	if err != nil {
		return nil, err
	}

	k := string(kind)
	return gen.GetNoteXML200ApplicationxmlResponse{
		Body:          bytes.NewReader(xml),
		ContentLength: int64(len(xml)),
		Headers:       gen.GetNoteXML200ResponseHeaders{XFolioKind: &k},
	}, nil
}

func (s *server) ListNoteEvents(ctx context.Context, req gen.ListNoteEventsRequestObject) (gen.ListNoteEventsResponseObject, error) {
	n, found, err := s.ownNote(ctx, req.Id)
	if err != nil || !found {
		return gen.ListNoteEvents404JSONResponse{NotFoundJSONResponse: notFound("nota")}, err
	}

	events, err := s.Notes.Events(ctx, n.ID)
	if err != nil {
		return nil, err
	}

	out := gen.EventList{Events: make([]gen.Event, 0, len(events))}
	for _, e := range events {
		out.Events = append(out.Events, gen.Event{
			Type:        e.Type,
			Seq:         e.Seq,
			Description: e.Description,
			At:          e.At,
			Origin:      gen.EventOrigin(e.Origin),
			Cstat:       e.CStat,
		})
	}
	return gen.ListNoteEvents200JSONResponse(out), nil
}

// RequestCiencia queues the Ciência of a note for the next scan. It is for
// accounts with the automatic Ciência off, or for a Ciência that failed.
func (s *server) RequestCiencia(ctx context.Context, req gen.RequestCienciaRequestObject) (gen.RequestCienciaResponseObject, error) {
	n, found, err := s.ownNote(ctx, req.Id)
	if err != nil || !found {
		return gen.RequestCiencia404JSONResponse{NotFoundJSONResponse: notFound("nota")}, err
	}

	a, err := s.Accounts.ByID(ctx, n.AccountID)
	if err != nil {
		return nil, err
	}

	conflict := func(msg string) gen.RequestCienciaResponseObject {
		return gen.RequestCiencia409JSONResponse{ConflictJSONResponse: gen.ConflictJSONResponse(errorBody(msg))}
	}
	switch {
	case n.Ciencia == note.CienciaFailed:
	case n.Ciencia == note.CienciaNotApplicable && !a.AutoCiencia:
	case n.Ciencia == note.CienciaNotApplicable:
		return conflict("a conta faz a Ciência sozinha; esta nota não precisa dela"), nil
	default:
		return conflict("a Ciência desta nota já está " + string(n.Ciencia)), nil
	}

	// The change takes a new cursor number, so it runs under the account
	// lock, like the scan.
	err = db.WithTx(ctx, s.DB, func(tx *sqlx.Tx) error {
		if _, err := s.Accounts.Lock(ctx, tx, a.ID); err != nil {
			return err
		}
		return s.Notes.RequestCiencia(ctx, tx, n.ID)
	})
	if errors.Is(err, note.ErrConflict) {
		return conflict("a nota já está completa ou cancelada"), nil
	}
	if err != nil {
		return nil, err
	}

	n, err = s.Notes.ForClient(ctx, client(ctx).ID, n.ID)
	if err != nil {
		return nil, err
	}
	return gen.RequestCiencia202JSONResponse(noteView(n)), nil
}

// ownNote returns the note id when its account belongs to the client of ctx.
func (s *server) ownNote(ctx context.Context, rawID string) (note.Note, bool, error) {
	id, err := parseID(rawID)
	if err != nil {
		return note.Note{}, false, err
	}

	n, err := s.Notes.ForClient(ctx, client(ctx).ID, id)
	if errors.Is(err, note.ErrNotFound) {
		return note.Note{}, false, nil
	}
	if err != nil {
		return note.Note{}, false, err
	}
	return n, true, nil
}

func noteView(n note.Note) gen.Note {
	return gen.Note{
		Id:         n.ID.String(),
		Key:        n.Key,
		EmitCnpj:   n.EmitCNPJ,
		EmitName:   n.EmitName,
		TotalCents: n.TotalCents,
		IssuedAt:   n.IssuedAt,
		Status:     gen.NoteStatus(n.Status),
		Ciencia:    gen.CienciaStatus(n.Ciencia),
		HasFullXml: n.HasFull,
		Seq:        n.Seq,
	}
}
