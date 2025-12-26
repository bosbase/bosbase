package core

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bosbase/bosbase-enterprise/tools/security"
	"github.com/bosbase/bosbase-enterprise/tools/types"
	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-ozzo/ozzo-validation/v4/is"
)

const (
	defaultTrialDuration          = 30 * 24 * time.Hour
	activationPublicKeyEnvKey     = "PB_ACTIVATION_PUBLIC_KEY"
	activationAlgEnvKey           = "PB_ACTIVATION_ALG"
	activationVerifyURLEnvKey     = "PB_ACTIVATION_VERIFY_URL"
	activationSealEnvKey          = "PB_ACTIVATION_SEAL_KEY"
	activationModeOffline         = "offline"
	activationModeOnline          = "online"
	activationSignatureSeparator  = "."
	activationDefaultStatusRemark = "Activation pending"
)

// ActivationConfig keeps the persisted activation state.
type ActivationConfig struct {
	TrialStartedAt      types.DateTime `form:"trialStartedAt" json:"trialStartedAt"`
	TrialExpiresAt      types.DateTime `form:"trialExpiresAt" json:"trialExpiresAt"`
	ActivationEmail     string         `form:"activationEmail" json:"activationEmail"`
	ActivationMode      string         `form:"activationMode" json:"activationMode"`
	ActivationCodeHash  string         `form:"activationCodeHash" json:"activationCodeHash,omitempty"`
	ActivationSeal      string         `form:"activationSeal" json:"activationSeal,omitempty"`
	ActivatedAt         types.DateTime `form:"activatedAt" json:"activatedAt"`
	LastVerifiedAt      types.DateTime `form:"lastVerifiedAt" json:"lastVerifiedAt"`
	SubscriptionExpires types.DateTime `form:"subscriptionExpires" json:"subscriptionExpires"`
	LastStatusMessage   string         `form:"lastStatusMessage" json:"lastStatusMessage"`
}

// ActivationStatus represents a safe, computed view of the activation state.
type ActivationStatus struct {
	Activated           bool           `json:"activated"`
	ActivationEmail     string         `json:"activationEmail"`
	ActivationMode      string         `json:"activationMode"`
	SubscriptionExpires types.DateTime `json:"subscriptionExpires"`
	TrialStartedAt      types.DateTime `json:"trialStartedAt"`
	TrialExpiresAt      types.DateTime `json:"trialExpiresAt"`
	IsTrial             bool           `json:"isTrial"`
	IsExpired           bool           `json:"isExpired"`
	RequiresActivation  bool           `json:"requiresActivation"`
	Message             string         `json:"message"`
}

// ActivationCodePayload describes the signed content embedded in activation codes.
type ActivationCodePayload struct {
	Email     string `json:"email"`
	Mode      string `json:"mode"`
	ExpiresAt string `json:"expiresAt"`
	Nonce     string `json:"nonce,omitempty"`
	Alg       string `json:"alg,omitempty"`
}

// ActivationVerificationResult contains the parsed, verified activation payload.
type ActivationVerificationResult struct {
	Email      string
	Mode       string
	ExpiresAt  types.DateTime
	VerifiedAt types.DateTime
	CodeHash   string
	Status     ActivationStatus
}

func newDefaultActivationConfig() ActivationConfig {
	return ActivationConfig{
		ActivationMode:    activationModeOffline,
		LastStatusMessage: activationDefaultStatusRemark,
	}
}

func (c *ActivationConfig) ensureDefaults() {
	if c.ActivationMode == "" {
		c.ActivationMode = activationModeOffline
	}
}

// StartTrialIfUnset sets the trial window starting from the provided timestamp.
// Returns true when a change was made.
func (c *ActivationConfig) StartTrialIfUnset(start time.Time) bool {
	if !c.TrialStartedAt.IsZero() {
		return false
	}

	var started types.DateTime
	_ = started.Scan(start)

	var expires types.DateTime
	_ = expires.Scan(start.Add(defaultTrialDuration))

	c.TrialStartedAt = started
	c.TrialExpiresAt = expires
	return true
}

// ApplyVerification commits a successful verification result onto the config.
func (c *ActivationConfig) ApplyVerification(result ActivationVerificationResult) {
	c.ActivationEmail = result.Email
	c.ActivationMode = result.Mode
	c.SubscriptionExpires = result.ExpiresAt
	c.ActivatedAt = result.VerifiedAt
	c.LastVerifiedAt = result.VerifiedAt
	c.ActivationCodeHash = result.CodeHash
	c.ActivationSeal = c.generateSeal(activationSealSecret())
	c.LastStatusMessage = result.Status.Message
}

// Status returns a computed activation status.
func (c ActivationConfig) Status(now time.Time) ActivationStatus {
	status := ActivationStatus{
		ActivationEmail:     c.ActivationEmail,
		ActivationMode:      strings.ToLower(strings.TrimSpace(c.ActivationMode)),
		SubscriptionExpires: c.SubscriptionExpires,
		TrialStartedAt:      c.TrialStartedAt,
		TrialExpiresAt:      c.TrialExpiresAt,
		Message:             activationDefaultStatusRemark,
	}

	if seal := strings.TrimSpace(c.ActivationSeal); seal != "" {
		secret := activationSealSecret()
		if secret == "" || !c.verifySeal(secret) {
			status.IsExpired = true
			status.RequiresActivation = true
			status.Message = "Activation data integrity check failed"
			return status
		}
	}

	inTrial := !c.TrialStartedAt.IsZero() && (c.TrialExpiresAt.IsZero() || now.Before(c.TrialExpiresAt.Time()) || now.Equal(c.TrialExpiresAt.Time()))
	inTrial = inTrial && c.SubscriptionExpires.IsZero()
	status.IsTrial = inTrial

	activated := !c.SubscriptionExpires.IsZero() && now.Before(c.SubscriptionExpires.Time())
	status.Activated = activated

	expired := false
	if !c.SubscriptionExpires.IsZero() && now.After(c.SubscriptionExpires.Time()) {
		expired = true
	}
	if !c.SubscriptionExpires.IsZero() && now.Equal(c.SubscriptionExpires.Time()) {
		expired = true
	}
	if !activated && !c.TrialExpiresAt.IsZero() && now.After(c.TrialExpiresAt.Time()) {
		expired = true
	}
	status.IsExpired = expired

	status.RequiresActivation = expired || (!activated && !inTrial)

	switch {
	case activated:
		status.Message = fmt.Sprintf("Subscription active until %s", c.SubscriptionExpires.String())
	case inTrial:
		status.Message = fmt.Sprintf("Trial active until %s", c.TrialExpiresAt.String())
	default:
		if expired {
			if !c.SubscriptionExpires.IsZero() {
				status.Message = fmt.Sprintf("Subscription expired on %s", c.SubscriptionExpires.String())
			} else if !c.TrialExpiresAt.IsZero() {
				status.Message = fmt.Sprintf("Trial expired on %s", c.TrialExpiresAt.String())
			} else {
				status.Message = "Activation required"
			}
		} else {
			status.Message = "Activation required"
		}
	}

	return status
}

// Validate implements validation.Validatable.
func (c ActivationConfig) Validate() error {
	return validation.ValidateStruct(&c,
		validation.Field(&c.ActivationEmail, validation.When(c.ActivationEmail != "", is.EmailFormat)),
		validation.Field(&c.ActivationMode, validation.In("", activationModeOffline, activationModeOnline)),
	)
}

// VerifyActivationCode performs offline verification of the activation code.
// The code format is "<payloadBase64>.<signatureBase64>", where payload is a JSON ActivationCodePayload.
func VerifyActivationCode(app App, email, code string) (ActivationVerificationResult, error) {
	result := ActivationVerificationResult{}

	parts := strings.Split(code, activationSignatureSeparator)
	if len(parts) != 2 {
		return result, errors.New("invalid activation code format")
	}

	payloadRaw, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return result, errors.New("invalid activation code payload")
	}

	sig, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return result, errors.New("invalid activation code signature")
	}

	payload := ActivationCodePayload{}
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		return result, errors.New("failed to parse activation payload")
	}

	if payload.Mode == "" {
		payload.Mode = activationModeOffline
	}
	payload.Mode = strings.ToLower(payload.Mode)

	alg := strings.ToLower(strings.TrimSpace(payload.Alg))
	if alg == "" {
		alg = strings.ToLower(strings.TrimSpace(os.Getenv(activationAlgEnvKey)))
		if alg == "" {
			alg = "ed25519"
		}
	}

	publicKey, err := loadActivationPublicKey(alg)
	if err != nil {
		return result, err
	}

	if err := verifySignature(publicKey, alg, payloadRaw, sig); err != nil {
		return result, err
	}

	expiration, err := types.ParseDateTime(payload.ExpiresAt)
	if err != nil {
		return result, errors.New("invalid activation payload expiration")
	}

	now := time.Now()
	if !expiration.IsZero() && now.After(expiration.Time()) {
		return result, errors.New("activation code expired")
	}

	if email != "" && !strings.EqualFold(strings.TrimSpace(email), strings.TrimSpace(payload.Email)) {
		return result, errors.New("activation email mismatch")
	}

	result.Email = payload.Email
	result.Mode = payload.Mode
	result.ExpiresAt = expiration
	result.VerifiedAt = types.NowDateTime()
	result.CodeHash = hashActivationCode(code)
	result.Status = ActivationStatus{
		Activated:           true,
		ActivationEmail:     payload.Email,
		ActivationMode:      payload.Mode,
		SubscriptionExpires: expiration,
		Message:             fmt.Sprintf("Subscription active until %s", expiration.String()),
	}

	// placeholder for optional online verification (non-blocking)
	verifyURL := strings.TrimSpace(os.Getenv(activationVerifyURLEnvKey))
	if verifyURL != "" && payload.Mode == activationModeOnline {
		if remoteExpiry, ok := tryOnlineActivationVerification(app, verifyURL, payload.Email, code); ok {
			if !remoteExpiry.IsZero() {
				result.ExpiresAt = remoteExpiry
				result.Status.SubscriptionExpires = remoteExpiry
				result.Status.Message = fmt.Sprintf("Subscription active until %s", remoteExpiry.String())
			}
		}
	}

	return result, nil
}

func verifySignature(publicKey any, alg string, payload, signature []byte) error {
	switch key := publicKey.(type) {
	case ed25519.PublicKey:
		if alg != "" && alg != "ed25519" {
			return fmt.Errorf("activation alg mismatch: expected ed25519 got %s", alg)
		}
		if !ed25519.Verify(key, payload, signature) {
			return errors.New("activation code signature mismatch")
		}
		return nil
	case *rsa.PublicKey:
		if alg == "" {
			alg = "rsa-pss"
		}
		hashed := sha256.Sum256(payload)
		switch alg {
		case "rsa", "rsa-pss":
			err := rsa.VerifyPSS(key, crypto.SHA256, hashed[:], signature, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthAuto, Hash: crypto.SHA256})
			if err != nil {
				return errors.New("activation code signature mismatch")
			}
			return nil
		default:
			return fmt.Errorf("unsupported activation alg %s for RSA key", alg)
		}
	default:
		return errors.New("unsupported activation public key type")
	}
}

func loadActivationPublicKey(alg string) (any, error) {
	keyRaw := strings.TrimSpace(os.Getenv(activationPublicKeyEnvKey))
	if keyRaw == "" {
		// default fallback key
		keyRaw = "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAQHkM/M34UK1mzyxgSNMKv1hFucs9SDYgqjkwW5srvzM=\n-----END PUBLIC KEY-----"
	}

	if !strings.Contains(keyRaw, "BEGIN") {
		if content, err := os.ReadFile(keyRaw); err == nil {
			keyRaw = string(content)
		}
	}

	block, _ := pem.Decode([]byte(keyRaw))
	if block == nil {
		return nil, errors.New("invalid activation public key (expected PEM)")
	}

	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err == nil {
		switch k := parsed.(type) {
		case ed25519.PublicKey:
			return k, nil
		case *rsa.PublicKey:
			return k, nil
		default:
			return nil, errors.New("unsupported activation public key type")
		}
	}

	if strings.HasPrefix(strings.ToLower(alg), "rsa") {
		if rsaKey, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
			return rsaKey, nil
		}
	}

	return nil, fmt.Errorf("failed to parse activation public key: %w", err)
}

func hashActivationCode(code string) string {
	hasher := sha256.Sum256([]byte(code))
	return base64.StdEncoding.EncodeToString(hasher[:])
}

func (c ActivationConfig) generateSeal(secret string) string {
	if secret == "" || c.ActivationCodeHash == "" {
		return ""
	}

	payload := strings.Join([]string{
		strings.TrimSpace(c.ActivationEmail),
		strings.ToLower(strings.TrimSpace(c.ActivationMode)),
		c.SubscriptionExpires.String(),
		c.ActivationCodeHash,
	}, "|")

	return security.HS256(payload, secret)
}

func (c ActivationConfig) verifySeal(secret string) bool {
	if secret == "" || c.ActivationSeal == "" {
		return true
	}

	expected := c.generateSeal(secret)

	return expected != "" && security.Equal(expected, c.ActivationSeal)
}

func activationSealSecret() string {
	return strings.TrimSpace(os.Getenv(activationSealEnvKey))
}

func tryOnlineActivationVerification(app App, verifyURL, email, code string) (types.DateTime, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	requestBody, _ := json.Marshal(map[string]string{
		"email": email,
		"code":  code,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, verifyURL, bytes.NewReader(requestBody))
	if err != nil {
		app.Logger().Warn("Activation online verification request not initialized", "error", err.Error())
		return types.DateTime{}, false
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		app.Logger().Warn("Activation online verification request failed", "error", err.Error())
		return types.DateTime{}, false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		app.Logger().Warn("Activation online verification rejected", "status", resp.StatusCode)
		return types.DateTime{}, false
	}

	var remote struct {
		Valid     bool   `json:"valid"`
		ExpiresAt string `json:"expiresAt"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&remote); err != nil {
		app.Logger().Warn("Activation online verification payload invalid", "error", err.Error())
		return types.DateTime{}, false
	}

	if !remote.Valid {
		return types.DateTime{}, false
	}

	if remote.ExpiresAt == "" {
		return types.DateTime{}, true
	}

	expiration, err := types.ParseDateTime(remote.ExpiresAt)
	if err != nil {
		app.Logger().Warn("Activation online verification expiration invalid", "error", err.Error())
		return types.DateTime{}, true
	}

	return expiration, true
}
