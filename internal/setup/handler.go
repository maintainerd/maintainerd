package setup

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/maintainerd/core/internal/platform/authz"
	"github.com/maintainerd/core/internal/platform/response"
)

// setupTokenHeader is the HTTP header the wizard/operator presents the
// CORE_SETUP_TOKEN in — the HTTP twin of the x-setup-token gRPC metadata key
// Auth's own setup surface uses.
const setupTokenHeader = "X-Setup-Token"

// adminPermission is the permission an authenticated (non-setup-token) caller
// must carry to read the FULL setup status. It is the blanket admin grant the
// setup flow itself registers in Auth (EnsureResourceAPI).
const adminPermission = "core:admin"

// Gate is the setup surface's own guard. The setup endpoints CANNOT be
// token-guarded by the platform middleware — they exist to provision the very
// Auth that would mint those tokens — so they carry their own shared-secret
// gate instead:
//
//   - Token is CORE_SETUP_TOKEN, compared in constant time. Empty token
//     outside development means the trigger endpoint is DISABLED (fail
//     closed): an internet-reachable Core must never expose an
//     unauthenticated endpoint that can create tenants and admins.
//   - In development an empty token leaves the surface open, announced with a
//     loud boot warning.
//   - Verify (optional) lets a caller with a valid admin token read the full
//     status Result; without either credential /status returns only
//     {completed} — the control-plane IDs and JWKS are reconnaissance
//     material, not public data.
type Gate struct {
	Token  string
	Dev    bool
	Verify authz.VerifyFunc
}

// tokenMatches compares a presented setup token in constant time. Both sides
// are hashed first so the comparison is constant-time even across length
// mismatches (ConstantTimeCompare short-circuits on length).
func (g Gate) tokenMatches(presented string) bool {
	if g.Token == "" || presented == "" {
		return false
	}
	want := sha256.Sum256([]byte(g.Token))
	got := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1
}

// authorized reports whether the request may see/drive the full setup surface:
// the setup token, or a verified bearer token carrying core:admin.
func (g Gate) authorized(r *http.Request) bool {
	if g.tokenMatches(r.Header.Get(setupTokenHeader)) {
		return true
	}
	if g.Verify == nil {
		return false
	}
	header := r.Header.Get("Authorization")
	if len(header) < 8 || (header[:7] != "Bearer " && header[:7] != "bearer ") {
		return false
	}
	claims, err := g.Verify(r.Context(), header[7:])
	if err != nil {
		return false
	}
	return claims.HasPermission(adminPermission)
}

// Handler exposes the setup status + a manual trigger. Core also runs the
// orchestration on boot when SETUP_ENABLED is set; these endpoints let an
// operator (or the console) inspect and re-drive it.
type Handler struct {
	orch *Orchestrator
	gate Gate
}

func NewHandler(orch *Orchestrator, gate Gate) *Handler {
	if gate.Token == "" {
		if gate.Dev {
			slog.Warn("SECURITY: CORE_SETUP_TOKEN is not set — POST /api/v1/setup is UNAUTHENTICATED (development only)")
		} else {
			slog.Warn("CORE_SETUP_TOKEN is not set — POST /api/v1/setup is DISABLED outside development")
		}
	}
	return &Handler{orch: orch, gate: gate}
}

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/status", h.status)
	r.Post("/", h.trigger)
	return r
}

type statusResponse struct {
	Enabled        bool    `json:"enabled"`
	Completed      bool    `json:"completed"`
	DeploymentMode string  `json:"deployment_mode,omitempty"`
	Result         *Result `json:"result,omitempty"`
}

// slimStatus is all an anonymous caller learns: whether the first-run wizard
// should show. Everything else in the full status (control-plane IDs, client
// IDs, the signing JWKS, the deployment substrate) maps the install for an
// attacker and is gated behind the setup token or an admin token.
type slimStatus struct {
	Completed bool `json:"completed"`
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	completed, res := h.orch.Status(r.Context())
	if !h.gate.authorized(r) {
		response.Success(w, slimStatus{Completed: completed}, "")
		return
	}
	response.Success(w, statusResponse{
		Enabled:        h.orch.Enabled(),
		Completed:      completed,
		DeploymentMode: res.DeploymentMode,
		Result:         res,
	}, "")
}

func (h *Handler) trigger(w http.ResponseWriter, r *http.Request) {
	// Gate first, before reading the body: this endpoint drives tenant/admin
	// creation in Auth. Empty CORE_SETUP_TOKEN outside development = disabled
	// entirely (fail closed); in development it stays open with the boot
	// warning NewHandler already emitted.
	if h.gate.Token == "" {
		if !h.gate.Dev {
			response.Error(w, http.StatusForbidden,
				"setup is disabled: CORE_SETUP_TOKEN is not set (required outside development)")
			return
		}
	} else if !h.gate.authorized(r) {
		response.Error(w, http.StatusUnauthorized, "missing or invalid setup token")
		return
	}

	// Body is optional: the wizard sends the tenant + admin; an empty body falls
	// back to the env-configured defaults.
	var in RunInput
	if body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20)); len(body) > 0 {
		if err := json.Unmarshal(body, &in); err != nil {
			response.BadRequestBody(w)
			return
		}
	}
	if in.AdminPassword == "" && !h.orch.hasEnvAdminPassword() {
		response.ValidationError(w, errors.New("admin_password is required"))
		return
	}
	res, err := h.orch.RunWith(r.Context(), in)
	if errors.Is(err, ErrSetupRunning) {
		response.Error(w, http.StatusConflict, "setup is already running")
		return
	}
	if err != nil {
		// Surface the real reason (e.g. Auth's "password is a common weak
		// password") so the wizard can show it, not a generic "setup failed".
		msg := err.Error()
		httpStatus := http.StatusBadGateway
		if st, ok := status.FromError(err); ok {
			msg = st.Message()
			switch st.Code() {
			case codes.InvalidArgument, codes.AlreadyExists, codes.FailedPrecondition:
				httpStatus = http.StatusBadRequest
			case codes.Unavailable, codes.DeadlineExceeded:
				httpStatus = http.StatusBadGateway
			default:
				httpStatus = http.StatusBadGateway
			}
		}
		response.Error(w, httpStatus, msg)
		return
	}
	response.Success(w, res, "setup complete")
}
