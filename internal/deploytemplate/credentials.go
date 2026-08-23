package deploytemplate

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/maintainerd/core/internal/platform/apperror"
)

// CredentialSink is where a `secret`-typed parameter's value is written.
// *secretctrl.Credentials satisfies it.
//
// It exists because a rendered WorkloadSpec is PERSISTED and SHIPPED to an
// agent, and kit states the rule on WorkloadSpec.RegistryCredentialRef:
// credentials never travel in a spec, a name is inert where a value is a leak.
// A secret-typed parameter is the one place a credential would otherwise enter
// Env, so it goes to the vault and the spec carries the reference.
type CredentialSink interface {
	// Store writes value and returns the reference the spec carries in its
	// place. It must return an error — never a usable reference and never the
	// value — when the vault is unreachable.
	Store(ctx context.Context, template, param string, value []byte) (string, error)
}

// ErrSecretMaterialUnavailable means this render had to write a credential into
// maintainerd-secret and could not.
//
// IT FAILS THE PROVISION. There is deliberately no fallback: inlining the value
// as a plain environment variable is exactly how a credential ends up in a
// container's environment while the operator believes it is in the vault, and it
// would be invisible — the provision succeeds, the workload runs, and only an
// audit finds it. A refused provision is loud and recoverable.
var ErrSecretMaterialUnavailable = errors.New("deploytemplate: a secret-typed parameter could not be written to maintainerd-secret")

// generatedSecretBytes is the entropy of a credential this package mints: 32
// bytes, the same width as an AES-256 key, rendered base64url so it survives an
// environment variable, a shell and a URL unquoted.
const generatedSecretBytes = 32

// vaultSecrets resolves every `secret`-typed parameter by writing its value to
// the vault and replacing it in params with the returned reference.
//
// It runs BETWEEN resolveParams and rendering, which is what makes the guarantee
// structural rather than a convention: by the time the spec template is
// executed, no plaintext is in params, so no code path can put one in the spec.
// Values live as []byte here and are never converted to a string, so the only
// copy is the one handed to the sink.
func (s *Service) vaultSecrets(ctx context.Context, tpl *Template, params map[string]any) error {
	for _, p := range tpl.Parameters {
		if p.Type != "secret" {
			continue
		}
		value, err := secretValue(p, params[p.Name])
		if err != nil {
			return err
		}
		if value == nil {
			// Absent, optional and not generated: nothing to store. A template
			// referencing it still fails at render (missingkey=error), which is the
			// honest outcome.
			delete(params, p.Name)
			continue
		}
		if s.credentials == nil {
			// No sink at all — development with no vault configured. The refusal is
			// identical to an unreachable one on purpose: there is no third branch
			// in which the value reaches the spec.
			return apperror.NewServiceUnavailable(fmt.Sprintf(
				"%s: parameter %q needs a vault and none is configured (SECRET_BASE_URL)",
				ErrSecretMaterialUnavailable, p.Name))
		}
		ref, err := s.credentials.Store(ctx, tpl.Name, p.Name, value)
		if err != nil {
			// The parameter NAME and the failure are reportable; the value is not,
			// and it is not in err either — the sink's contract forbids it.
			return apperror.NewServiceUnavailable(fmt.Sprintf(
				"%s: parameter %q: %v", ErrSecretMaterialUnavailable, p.Name, err))
		}
		params[p.Name] = ref
	}
	return nil
}

// secretValue decides what a secret-typed parameter's plaintext is: what the
// caller supplied, a freshly generated value when the template says Generate, or
// nothing.
//
// A `Default` on a secret parameter is IGNORED and refused rather than honoured.
// A default credential is a hardcoded credential — every install that skipped
// the parameter would share it — so a template that ships one is a mistake worth
// surfacing at render rather than a convenience.
func secretValue(p Parameter, supplied any) ([]byte, error) {
	if s, ok := supplied.(string); ok && s != "" {
		return []byte(s), nil
	}
	if b, ok := supplied.([]byte); ok && len(b) > 0 {
		return b, nil
	}
	if p.Default != nil {
		return nil, apperror.NewValidation(fmt.Sprintf(
			"parameter %q is a secret and must not declare a default — set generate instead", p.Name))
	}
	if p.Generate {
		return generateSecret()
	}
	if p.Required {
		return nil, apperror.NewValidation(fmt.Sprintf("parameter %q is required", p.Name))
	}
	return nil, nil
}

func generateSecret() ([]byte, error) {
	raw := make([]byte, generatedSecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("deploytemplate: generate credential: %w", err)
	}
	out := make([]byte, base64.RawURLEncoding.EncodedLen(len(raw)))
	base64.RawURLEncoding.Encode(out, raw)
	return out, nil
}
