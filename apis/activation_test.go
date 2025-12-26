package apis_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tests"
	"github.com/bosbase/bosbase-enterprise/tools/types"
)

func TestActivationVerifyPublic(t *testing.T) {
	t.Parallel()

	email := "user@example.com"
	code, publicKey := makeActivationCode(t, email, time.Now().Add(2*time.Hour))

	scenario := tests.ApiScenario{
		Name:           "activation via public endpoint",
		Method:         http.MethodPost,
		URL:            "/api/activation/verify/public",
		Body:           strings.NewReader(fmt.Sprintf(`{"email":"%s","code":"%s","mode":"online"}`, email, code)),
		ExpectedStatus: 200,
		ExpectedContent: []string{
			"## Activation Verified",
			email,
			"Expires",
		},
		BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
			t.Setenv("PB_ACTIVATION_PUBLIC_KEY", publicKey)
			t.Setenv("PB_ACTIVATION_ALG", "ed25519")
		},
		AfterTestFunc: func(t testing.TB, app *tests.TestApp, res *http.Response) {
			status := app.Settings().CurrentActivationStatus(time.Now())
			if !status.Activated {
				t.Fatalf("expected activation to be stored")
			}
			if status.ActivationEmail != email {
				t.Fatalf("expected activation email %q, got %q", email, status.ActivationEmail)
			}
		},
	}

	scenario.Test(t)
}

func TestActivationVerifyPublicKeepsActiveOnBadCode(t *testing.T) {
	t.Parallel()

	email := "user2@example.com"
	validCode, publicKey := makeActivationCode(t, email, time.Now().Add(2*time.Hour))

	validScenario := tests.ApiScenario{
		Name:           "seed activation",
		Method:         http.MethodPost,
		URL:            "/api/activation/verify/public",
		Body:           strings.NewReader(fmt.Sprintf(`{"email":"%s","code":"%s"}`, email, validCode)),
		ExpectedStatus: 200,
		ExpectedContent: []string{
			email,
		},
		BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
			t.Setenv("PB_ACTIVATION_PUBLIC_KEY", publicKey)
			t.Setenv("PB_ACTIVATION_ALG", "ed25519")
		},
	}
	validScenario.Test(t)

	invalidCode := tamperActivationCode(t, validCode)

	scenario := tests.ApiScenario{
		Name:           "active account invalid code returns current status",
		Method:         http.MethodPost,
		URL:            "/api/activation/verify/public",
		Body:           strings.NewReader(fmt.Sprintf(`{"email":"%s","code":"%s"}`, email, invalidCode)),
		ExpectedStatus: 200,
		ExpectedContent: []string{
			email,
		},
		BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
			t.Setenv("PB_ACTIVATION_PUBLIC_KEY", publicKey)
			t.Setenv("PB_ACTIVATION_ALG", "ed25519")
		},
		AfterTestFunc: func(t testing.TB, app *tests.TestApp, res *http.Response) {
			status := app.Settings().CurrentActivationStatus(time.Now())
			if !status.Activated || status.IsExpired {
				t.Fatalf("expected status to remain active, got %#v", status)
			}
		},
	}

	scenario.Test(t)
}

func TestActivationStatusRequiresAuth(t *testing.T) {
	t.Parallel()

	scenario := tests.ApiScenario{
		Name:            "activation status requires auth",
		Method:          http.MethodGet,
		URL:             "/api/activation/status",
		ExpectedStatus:  401,
		ExpectedContent: []string{`"data":{}`},
		ExpectedEvents:  map[string]int{"*": 0},
	}

	scenario.Test(t)
}

func makeActivationCode(t testing.TB, email string, expires time.Time) (string, string) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	payload := core.ActivationCodePayload{
		Email:     email,
		Mode:      "online",
		ExpiresAt: expires.UTC().Format(types.DefaultDateLayout),
		Alg:       "ed25519",
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}

	signature := ed25519.Sign(priv, payloadBytes)

	code := base64.StdEncoding.EncodeToString(payloadBytes) + "." + base64.StdEncoding.EncodeToString(signature)

	pubPKIX, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("failed to marshal public key: %v", err)
	}

	publicKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubPKIX})

	return code, string(publicKeyPEM)
}

func tamperActivationCode(t testing.TB, code string) string {
	t.Helper()

	parts := strings.Split(code, ".")
	if len(parts) != 2 {
		t.Fatalf("unexpected code format")
	}

	sig, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("failed to decode signature: %v", err)
	}

	if len(sig) == 0 {
		t.Fatalf("signature empty")
	}

	// Flip the first byte to invalidate the signature while keeping base64 valid.
	sig[0] ^= 0xFF

	parts[1] = base64.StdEncoding.EncodeToString(sig)

	return strings.Join(parts, ".")
}
