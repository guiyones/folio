// Package httpapi serves the folio API described in openapi.yaml. The code
// in gen is generated from that file (make generate); handlers here only
// translate HTTP into calls to the account, note and apiclient packages.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"uuid"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/guiyones/folio/internal/account"
	"github.com/guiyones/folio/internal/apiclient"
	"github.com/guiyones/folio/internal/httpapi/gen"
	"github.com/guiyones/folio/internal/note"
	"github.com/guiyones/folio/internal/platform/crypt"
	"github.com/jmoiron/sqlx"
	middleware "github.com/oapi-codegen/nethttp-middleware"
)

// maxBody bounds every request; the largest is a certificate upload.
const maxBody = 2 << 20

// certWarn is how long before expiry a certificate shows expiring_soon.
const certWarn = 30 * 24 * time.Hour

// Deps is what the API needs.
type Deps struct {
	DB       *sqlx.DB
	Accounts *account.Store
	Notes    *note.Store
	Clients  *apiclient.Store
	Box      *crypt.Box
	Now      func() time.Time
	Log      *slog.Logger
}

// server implements gen.StrictServerInterface.
type server struct {
	Deps
}

// New returns the API handler: panic recovery, then the API key, then the
// contract validation, then the routes.
func New(d Deps) http.Handler {
	s := &server{Deps: d}

	strict := gen.NewStrictHandlerWithOptions(s, nil, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeError(w, http.StatusBadRequest, err.Error())
		},
		ResponseErrorHandlerFunc: s.internal,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "rota não encontrada")
	})
	routes := gen.HandlerWithOptions(strict, gen.StdHTTPServerOptions{
		BaseRouter: mux,
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeError(w, http.StatusBadRequest, err.Error())
		},
	})

	return s.recover(s.limitBody(s.auth(s.validate(routes))))
}

// validate checks each request against openapi.yaml. The API key is checked
// by auth, and the multipart body by the handler, which knows its limits.
func (s *server) validate(next http.Handler) http.Handler {
	spec, err := gen.GetSwagger()
	if err != nil {
		// The spec is embedded at build time; failing to read it is a bug.
		panic(fmt.Sprintf("reading the embedded openapi spec: %v", err))
	}
	spec.Servers = nil

	return middleware.OapiRequestValidatorWithOptions(spec, &middleware.Options{
		Options: openapi3filter.Options{
			ExcludeRequestBody: true,
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
		},
		ErrorHandler: func(w http.ResponseWriter, message string, status int) {
			if status == http.StatusNotFound {
				message = "rota não encontrada"
			}
			writeError(w, status, message)
		},
		DoNotValidateServers: true,
	})(next)
}

type clientKey struct{}

// auth resolves the API key into its client and puts it in the context.
func (s *server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			writeError(w, http.StatusUnauthorized, "chave de API inválida")
			return
		}

		c, err := s.Clients.ByKey(r.Context(), strings.TrimSpace(token))
		if errors.Is(err, apiclient.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "chave de API inválida")
			return
		}
		if err != nil {
			s.internal(w, r, err)
			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientKey{}, c)))
	})
}

func (s *server) limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		next.ServeHTTP(w, r)
	})
}

// recover turns a panic into a 500, so one bad request never takes the
// process down.
func (s *server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.internal(w, r, fmt.Errorf("panic: %v", v))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// internal logs err and answers a 500 without its details.
func (s *server) internal(w http.ResponseWriter, r *http.Request, err error) {
	s.Log.Error("request", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "erro interno")
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status line is already out; a failed write only means the client
	// went away.
	_ = json.NewEncoder(w).Encode(gen.Error{Error: message})
}

// client returns the API client of the request, set by auth.
func client(ctx context.Context) apiclient.Client {
	c, _ := ctx.Value(clientKey{}).(apiclient.Client) // always set: auth runs first
	return c
}

// parseID reads an id path parameter; the contract already checked its
// shape, so a failure here only comes from a bug.
func parseID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil(), fmt.Errorf("parsing id %q: %w", s, err)
	}
	return id, nil
}

func errorBody(msg string) gen.Error { return gen.Error{Error: msg} }
