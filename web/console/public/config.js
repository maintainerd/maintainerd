// Runtime configuration.
//
// A single built image targets a different API / identity origin per deployment
// by having this file REGENERATED at container start (or written by an init
// container, a ConfigMap, or the static host serving these files). The empty
// defaults here let the app fall back to build-time import.meta.env values
// during local development.
//
// THE KEYS ARE THE SERVICE'S OWN VARIABLE NAMES, on purpose. An operator creates
// one console SPA client in maintainerd-auth and is handed one client id; they
// set CORE_CONSOLE_CLIENT_ID once and both halves read the same value. AUTH_ISSUER
// and AUTH_AUDIENCE are literally the variables core's own process enforces, so a
// console configured against a different issuer than the service verifies is a
// mismatch an operator can see in one place instead of discovering in a browser.
// The VITE_* spellings below remain supported as build-time fallbacks and are
// what a local .env uses.
//
// Nothing here is a secret: the console is a PUBLIC OAuth client (authorization
// code + PKCE, S256) and has no client secret. Do NOT put CORE_SETUP_TOKEN, a
// client secret, or any other backend credential in this file — it is served to
// every browser that loads the console.
//
// Rendered from the environment, e.g.:
//
//   cat > "$CONSOLE_DIR/config.js" <<EOF
//   window.__ENV__ = {
//     VITE_CORE_API_BASE_URL: "${VITE_CORE_API_BASE_URL:-}",
//     AUTH_ISSUER: "${AUTH_ISSUER:-}",
//     CORE_CONSOLE_TOKEN_URL: "${CORE_CONSOLE_TOKEN_URL:-}",
//     CORE_CONSOLE_CLIENT_ID: "${CORE_CONSOLE_CLIENT_ID:-}",
//     AUTH_AUDIENCE: "${AUTH_AUDIENCE:-}",
//     CORE_CONSOLE_SCOPE: "${CORE_CONSOLE_SCOPE:-}"
//   };
//   EOF
window.__ENV__ = {
  // Where core's REST API lives. Empty means same-origin /api/v1.
  VITE_CORE_API_BASE_URL: "",

  // The identity trio. All three are required TOGETHER: a half-configured
  // identity would send the operator to an authorize endpoint whose code can
  // never be exchanged, so the console treats it as none.
  //
  // LEAVING ALL THREE EMPTY IS NOT A CLAIM THAT AUTH IS OFF. It means this
  // console cannot obtain a token. That is correct while core runs with
  // APP_ENV=development and no AUTH_JWKS_URL/AUTH_ISSUER/AUTH_AUDIENCE, because
  // its guard is dev-open and serves every caller — but if core IS enforcing,
  // every request is refused and the console says so instead of pretending.
  AUTH_ISSUER: "",
  CORE_CONSOLE_TOKEN_URL: "",
  CORE_CONSOLE_CLIENT_ID: "",

  // The resource-API audience core enforces (the service's AUTH_AUDIENCE). The
  // token has to be minted FOR core, or core's verifier rejects it — which
  // presents as a 401 on every call even though sign-in succeeded.
  AUTH_AUDIENCE: "",

  // Extra scopes beyond `openid profile email`. The core permissions a user
  // holds come from their Auth grants, not from what the console asks for.
  CORE_CONSOLE_SCOPE: "",

  // --- build-time spellings, still honoured -----------------------------
  VITE_OAUTH_ISSUER_URL: "",
  VITE_OAUTH_TOKEN_URL: "",
  VITE_OAUTH_CLIENT_ID: "",
  VITE_OAUTH_AUDIENCE: "",
  VITE_OAUTH_SCOPE: ""
};
