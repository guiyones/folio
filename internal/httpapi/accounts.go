package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/guiyones/folio/dfe"
	"github.com/guiyones/folio/internal/account"
	"github.com/guiyones/folio/internal/httpapi/gen"
)

// maxPFX bounds an uploaded certificate; a real A1 is a few KB.
const maxPFX = 1 << 20

// errTooBig says the upload passed maxPFX or the request body limit.
var errTooBig = errors.New("o certificado passa de 1 MB")

func (s *server) ListAccounts(ctx context.Context, _ gen.ListAccountsRequestObject) (gen.ListAccountsResponseObject, error) {
	accounts, err := s.Accounts.ListForClient(ctx, client(ctx).ID)
	if err != nil {
		return nil, err
	}

	out := gen.AccountList{Accounts: make([]gen.Account, 0, len(accounts))}
	for _, a := range accounts {
		view, err := s.accountView(ctx, a)
		if err != nil {
			return nil, err
		}
		out.Accounts = append(out.Accounts, view)
	}
	return gen.ListAccounts200JSONResponse(out), nil
}

func (s *server) GetAccount(ctx context.Context, req gen.GetAccountRequestObject) (gen.GetAccountResponseObject, error) {
	a, found, err := s.ownAccount(ctx, req.Id)
	if err != nil || !found {
		return gen.GetAccount404JSONResponse{NotFoundJSONResponse: notFound("conta")}, err
	}

	view, err := s.accountView(ctx, a)
	if err != nil {
		return nil, err
	}
	return gen.GetAccount200JSONResponse(view), nil
}

func (s *server) UploadCertificate(ctx context.Context, req gen.UploadCertificateRequestObject) (gen.UploadCertificateResponseObject, error) {
	a, found, err := s.ownAccount(ctx, req.Id)
	if err != nil || !found {
		return gen.UploadCertificate404JSONResponse{NotFoundJSONResponse: notFound("conta")}, err
	}

	pfx, password, err := readCertificateForm(req)
	if errors.Is(err, errTooBig) {
		return gen.UploadCertificate413JSONResponse{TooLargeJSONResponse: gen.TooLargeJSONResponse(errorBody(errTooBig.Error()))}, nil
	}
	if err != nil {
		return gen.UploadCertificate400JSONResponse{BadRequestJSONResponse: gen.BadRequestJSONResponse(errorBody(err.Error()))}, nil
	}

	sc, err := account.UploadCertificate(ctx, s.Accounts, s.Box, a, pfx, password, s.Now())
	var mismatch *account.CNPJMismatchError
	switch {
	case err == nil:
	case errors.Is(err, dfe.ErrBadPassword), errors.Is(err, dfe.ErrInvalidPFX), errors.Is(err, dfe.ErrNoCNPJ),
		errors.Is(err, dfe.ErrNotRSA), errors.Is(err, account.ErrExpired), errors.As(err, &mismatch):
		return gen.UploadCertificate422JSONResponse{UnprocessableJSONResponse: gen.UnprocessableJSONResponse(errorBody(err.Error()))}, nil
	case errors.Is(err, account.ErrNotFound):
		return gen.UploadCertificate404JSONResponse{NotFoundJSONResponse: notFound("conta")}, nil
	default:
		return nil, err
	}

	return gen.UploadCertificate200JSONResponse(certificateView(sc, s.Now())), nil
}

func (s *server) PauseAccount(ctx context.Context, req gen.PauseAccountRequestObject) (gen.PauseAccountResponseObject, error) {
	view, found, err := s.setPaused(ctx, req.Id, true)
	if err != nil || !found {
		return gen.PauseAccount404JSONResponse{NotFoundJSONResponse: notFound("conta")}, err
	}
	return gen.PauseAccount200JSONResponse(view), nil
}

func (s *server) ResumeAccount(ctx context.Context, req gen.ResumeAccountRequestObject) (gen.ResumeAccountResponseObject, error) {
	view, found, err := s.setPaused(ctx, req.Id, false)
	if err != nil || !found {
		return gen.ResumeAccount404JSONResponse{NotFoundJSONResponse: notFound("conta")}, err
	}
	return gen.ResumeAccount200JSONResponse(view), nil
}

// RequestScan answers whether a scan can run now. The scan loop already
// picks every account that is due within a minute, so there is nothing to
// change: a due account is accepted, one inside its hour is refused with
// the time it may run.
func (s *server) RequestScan(ctx context.Context, req gen.RequestScanRequestObject) (gen.RequestScanResponseObject, error) {
	a, found, err := s.ownAccount(ctx, req.Id)
	if err != nil || !found {
		return gen.RequestScan404JSONResponse{NotFoundJSONResponse: notFound("conta")}, err
	}
	if a.Paused {
		return gen.RequestScan409JSONResponse{ConflictJSONResponse: gen.ConflictJSONResponse(errorBody("a conta está pausada"))}, nil
	}
	if _, err := s.Accounts.Certificate(ctx, a.ID); errors.Is(err, account.ErrNoCertificate) {
		return gen.RequestScan409JSONResponse{ConflictJSONResponse: gen.ConflictJSONResponse(errorBody("a conta não tem certificado"))}, nil
	} else if err != nil {
		return nil, err
	}

	now := s.Now()
	if a.NextScanAt.After(now) {
		wait := int(math.Ceil(a.NextScanAt.Sub(now).Seconds()))
		return gen.RequestScan429JSONResponse{
			Body: gen.ScanTooSoon{
				Error:      fmt.Sprintf("a SEFAZ só pode ser consultada de novo às %s", a.NextScanAt.Format(time.RFC3339)),
				NextScanAt: a.NextScanAt,
			},
			Headers: gen.RequestScan429ResponseHeaders{RetryAfter: &wait},
		}, nil
	}

	return gen.RequestScan202JSONResponse{NextScanAt: a.NextScanAt}, nil
}

// =============================================================================

// ownAccount returns the account id when it belongs to the client of ctx.
func (s *server) ownAccount(ctx context.Context, rawID string) (account.Account, bool, error) {
	id, err := parseID(rawID)
	if err != nil {
		return account.Account{}, false, err
	}

	a, err := s.Accounts.ForClient(ctx, client(ctx).ID, id)
	if errors.Is(err, account.ErrNotFound) {
		return account.Account{}, false, nil
	}
	if err != nil {
		return account.Account{}, false, err
	}
	return a, true, nil
}

func (s *server) setPaused(ctx context.Context, rawID string, paused bool) (gen.Account, bool, error) {
	a, found, err := s.ownAccount(ctx, rawID)
	if err != nil || !found {
		return gen.Account{}, false, err
	}
	if err := s.Accounts.SetPaused(ctx, a.ID, paused); err != nil {
		return gen.Account{}, false, err
	}
	a.Paused = paused

	view, err := s.accountView(ctx, a)
	return view, err == nil, err
}

func (s *server) accountView(ctx context.Context, a account.Account) (gen.Account, error) {
	env := gen.Env("homologacao")
	if a.Env == dfe.Production {
		env = gen.Env("producao")
	}

	view := gen.Account{
		Id:          a.ID.String(),
		Name:        a.Name,
		Cnpj:        a.CNPJ,
		Uf:          a.UF,
		Env:         env,
		Paused:      a.Paused,
		AutoCiencia: a.AutoCiencia,
		UltNsu:      a.UltNSU,
		NextScanAt:  a.NextScanAt,
		LastScanAt:  a.LastScanAt,
		LastCstat:   a.LastCStat,
		LastMessage: a.LastMessage,
	}

	sc, err := s.Accounts.Certificate(ctx, a.ID)
	switch {
	case errors.Is(err, account.ErrNoCertificate):
	case err != nil:
		return gen.Account{}, err
	default:
		c := certificateView(sc, s.Now())
		view.Certificate = &c
	}
	return view, nil
}

// certificateView shows a certificate without its sealed file or password.
func certificateView(sc account.StoredCert, now time.Time) gen.Certificate {
	return gen.Certificate{
		Holder:       sc.Holder,
		Cnpj:         sc.CNPJ,
		NotAfter:     sc.NotAfter,
		UploadedAt:   sc.UploadedAt,
		Expired:      !sc.NotAfter.After(now),
		ExpiringSoon: sc.NotAfter.Sub(now) <= certWarn,
	}
}

// readCertificateForm reads the pfx and password parts of the upload. A
// file over the limit gives errTooBig.
func readCertificateForm(req gen.UploadCertificateRequestObject) (pfx []byte, password string, err error) {
	if req.Body == nil {
		return nil, "", errors.New("envie um multipart/form-data com pfx e password")
	}

	var gotPFX, gotPass bool
	for {
		part, err := req.Body.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return nil, "", errTooBig
		}
		if err != nil {
			return nil, "", fmt.Errorf("lendo o formulário: %w", err)
		}

		switch part.FormName() {
		case "pfx":
			pfx, err = io.ReadAll(io.LimitReader(part, maxPFX+1))
			if errors.As(err, &maxErr) || len(pfx) > maxPFX {
				return nil, "", errTooBig
			}
			gotPFX = true
		case "password":
			var b []byte
			b, err = io.ReadAll(io.LimitReader(part, 1024))
			password = string(b)
			gotPass = true
		}
		if err != nil {
			return nil, "", fmt.Errorf("lendo o formulário: %w", err)
		}
	}

	if !gotPFX || !gotPass {
		return nil, "", errors.New("o formulário precisa dos campos pfx e password")
	}
	return pfx, password, nil
}

func notFound(what string) gen.NotFoundJSONResponse {
	return gen.NotFoundJSONResponse(errorBody(what + " não encontrada"))
}
