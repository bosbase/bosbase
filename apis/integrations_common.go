package apis

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/security"
)

// Shared helpers for the user configurable integrations (object storages and LLM channels).

const (
	secretEncryptedPrefix = "enc:"
	secretPlainPrefix     = "plain:"
)

// encryptIntegrationSecret encrypts a secret with the app encryption key (if configured).
func encryptIntegrationSecret(app core.App, secret string) (string, error) {
	if secret == "" {
		return "", nil
	}
	key := core.EncryptionKey(app)
	if key == "" {
		// not configured or invalid -> stored unencrypted
		return secretPlainPrefix + secret, nil
	}
	encrypted, err := security.Encrypt([]byte(secret), key)
	if err != nil {
		return "", err
	}
	return secretEncryptedPrefix + encrypted, nil
}

// decryptIntegrationSecret reverses encryptIntegrationSecret.
func decryptIntegrationSecret(app core.App, stored string) (string, error) {
	switch {
	case stored == "":
		return "", nil
	case strings.HasPrefix(stored, secretPlainPrefix):
		return strings.TrimPrefix(stored, secretPlainPrefix), nil
	case strings.HasPrefix(stored, secretEncryptedPrefix):
		key := core.EncryptionKey(app)
		if key == "" {
			return "", errors.New("the secret is encrypted but the encryption key is missing or invalid (" + app.EncryptionEnv() + ")")
		}
		decrypted, err := security.Decrypt(strings.TrimPrefix(stored, secretEncryptedPrefix), key)
		if err != nil {
			return "", errors.New("failed to decrypt the secret (was it encrypted with a different key?): " + err.Error())
		}
		return string(decrypted), nil
	default:
		return stored, nil
	}
}

// maskSecret returns a non reversible hint of a secret (eg. "sk-…0598").
func maskSecret(secret string) string {
	if secret == "" {
		return ""
	}
	r := []rune(secret)
	if len(r) <= 8 {
		return "****"
	}
	return string(r[:3]) + "…" + string(r[len(r)-4:])
}

// integrationOwner is the owner of a config/row.
type integrationOwner struct {
	id         string
	collection string
}

func (o integrationOwner) shared() bool { return o.id == "" }

// resolveIntegrationOwner returns the owner for new configs.
//
// Superusers can create shared configs (shared=true) or configs for another auth record (ownerId).
func resolveIntegrationOwner(e *core.RequestEvent, shared bool, ownerId string) (integrationOwner, error) {
	owner := integrationOwner{id: e.Auth.Id, collection: e.Auth.Collection().Name}

	if !e.HasSuperuserAuth() {
		if shared || (ownerId != "" && ownerId != e.Auth.Id) {
			return owner, e.ForbiddenError("Only superusers can manage shared configs or configs of other users.", nil)
		}
		return owner, nil
	}

	if shared {
		return integrationOwner{}, nil
	}
	if ownerId != "" && ownerId != e.Auth.Id {
		record, err := mcpFindAuthRecord(e.App, ownerId)
		if err != nil {
			return owner, e.BadRequestError("Unknown ownerId.", nil)
		}
		return integrationOwner{id: record.Id, collection: record.Collection().Name}, nil
	}

	return owner, nil
}

// integrationReadOwners returns the owners whose configs are usable by the request auth.
func integrationReadOwners(e *core.RequestEvent) []string {
	return []string{e.Auth.Id, ""}
}

// canManageIntegration reports whether the request auth can modify a config of the provided owner.
func canManageIntegration(e *core.RequestEvent, owner string) bool {
	if e.HasSuperuserAuth() {
		return true
	}
	return owner != "" && owner == e.Auth.Id
}

// isTrustedIntegrationOwner reports whether the config owner may reach private network addresses.
func isTrustedIntegrationOwner(ownerId, ownerCollection string) bool {
	if ownerId == "" || ownerCollection == core.CollectionNameSuperusers {
		return true
	}
	v := strings.ToLower(strings.TrimSpace(os.Getenv("BOSBASE_ALLOW_PRIVATE_UPSTREAMS")))
	return v == "1" || v == "true" || v == "yes"
}

var errPrivateUpstream = errors.New("connections to private network addresses are not allowed for user configured upstreams")

// newIntegrationHTTPClient returns an HTTP client for user configured upstreams.
//
// For untrusted owners the client refuses to connect to loopback, private,
// link-local and other non public addresses (SSRF protection), including after redirects.
func newIntegrationHTTPClient(trusted bool, timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	if !trusted {
		dialer.Control = func(network, address string, c syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || !isPublicIP(ip) {
				return errPrivateUpstream
			}
			return nil
		}
	}

	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if !trusted {
		// a proxy would bypass the dial checks
		transport.Proxy = nil
	}

	return &http.Client{Transport: transport, Timeout: timeout}
}

func isPublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	// carrier-grade NAT 100.64.0.0/10
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1]&0xc0 == 64 {
		return false
	}
	return true
}

// validateUpstreamURL validates a user provided upstream base URL.
func validateUpstreamURL(raw string, allowEmpty bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if allowEmpty {
			return "", nil
		}
		return "", errors.New("the URL is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid URL %q", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	if u.User != nil {
		return "", errors.New("credentials in the URL are not allowed")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// isPrivateUpstreamErr reports whether err was caused by the SSRF protection.
func isPrivateUpstreamErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), errPrivateUpstream.Error())
}

func requestContextWithTimeout(e *core.RequestEvent, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(e.Request.Context(), d)
}

// redactLogValue masks the values of sensitive keys (passwords, tokens, API keys...)
// before they are written to the logs.
func redactLogValue(key string, v any) any {
	lowerKey := strings.ToLower(key)
	for _, s := range mcpSensitiveKeys {
		if strings.Contains(lowerKey, s) {
			return "***"
		}
	}
	switch vv := v.(type) {
	case map[string]any:
		result := make(map[string]any, len(vv))
		for k, item := range vv {
			result[k] = redactLogValue(k, item)
		}
		return result
	case []any:
		result := make([]any, len(vv))
		for i, item := range vv {
			result[i] = redactLogValue("", item)
		}
		return result
	}
	return v
}
