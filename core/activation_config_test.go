package core

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"testing"
	"time"

	"github.com/bosbase/bosbase-enterprise/tools/types"
)

// TestVerifyActivationCodeEd25519 ensures activation codes signed with a valid
// Ed25519 private key and the expected payload shape are accepted when the
// matching public key is configured.
func TestVerifyActivationCodeEd25519(t *testing.T) {
	// deterministic key for test
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	publicKey := privateKey.Public().(ed25519.PublicKey)

	publicKeyDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatalf("failed to marshal public key: %v", err)
	}
	publicKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicKeyDER})

	t.Setenv(activationPublicKeyEnvKey, string(publicKeyPEM))
	t.Setenv(activationAlgEnvKey, "ed25519")

	expiresAt := time.Now().Add(time.Hour).UTC().Format(types.DefaultDateLayout)
	payload := ActivationCodePayload{
		Email:     "user@example.com",
		Mode:      activationModeOffline,
		ExpiresAt: expiresAt,
		Alg:       "ed25519",
	}

	payloadRaw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}

	signature := ed25519.Sign(privateKey, payloadRaw)
	code := base64.StdEncoding.EncodeToString(payloadRaw) + activationSignatureSeparator + base64.StdEncoding.EncodeToString(signature)

	result, err := VerifyActivationCode((*BaseApp)(nil), payload.Email, code)
	if err != nil {
		t.Fatalf("expected verification to succeed, got error: %v", err)
	}

	if result.Email != payload.Email {
		t.Fatalf("email mismatch: expected %s, got %s", payload.Email, result.Email)
	}
	if result.Mode != payload.Mode {
		t.Fatalf("mode mismatch: expected %s, got %s", payload.Mode, result.Mode)
	}
	if result.ExpiresAt.String() != expiresAt {
		t.Fatalf("expiresAt mismatch: expected %s, got %s", expiresAt, result.ExpiresAt.String())
	}
}

func TestActivationStatusDetectsTampering(t *testing.T) {
	t.Setenv(activationSealEnvKey, "super-secret-seal")

	expiresAt, _ := types.ParseDateTime(time.Now().Add(4 * time.Hour))

	config := ActivationConfig{}
	config.ApplyVerification(ActivationVerificationResult{
		Email:      "user@example.com",
		Mode:       activationModeOffline,
		ExpiresAt:  expiresAt,
		VerifiedAt: types.NowDateTime(),
		CodeHash:   "hash123",
		Status: ActivationStatus{
			Activated:           true,
			ActivationEmail:     "user@example.com",
			ActivationMode:      activationModeOffline,
			SubscriptionExpires: expiresAt,
			Message:             "ok",
		},
	})

	// sanity check: valid seal yields active status
	status := config.Status(time.Now())
	if !status.Activated {
		t.Fatalf("expected activated status before tampering")
	}

	// simulate tampering by changing expiry without updating seal
	config.SubscriptionExpires, _ = types.ParseDateTime(time.Now().Add(720 * time.Hour))

	tampered := config.Status(time.Now())
	if tampered.Activated || !tampered.RequiresActivation {
		t.Fatalf("expected tampered config to require activation, got %#v", tampered)
	}
	if tampered.Message != "Activation data integrity check failed" {
		t.Fatalf("unexpected tamper message: %s", tampered.Message)
	}
}
