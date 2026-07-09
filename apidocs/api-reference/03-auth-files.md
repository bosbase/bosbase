# Authentication and Files

This document covers auth collection endpoints, OAuth2 redirect callbacks, and file access endpoints.

## Shared Auth Response

Several auth endpoints return the same response shape.

### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `token` | string | Auth JWT/token for subsequent requests. | `eyJhbGciOi...` |
| `record` | object | Auth record object. | `{ "id": "user_1", "email": "a@example.com" }` |
| `meta` | object | Optional metadata, mostly from OAuth2 providers or custom hooks. | `{ "isNew": true }` |

### Response Example

```json
{
  "token": "eyJhbGciOi...",
  "record": {
    "id": "user_1",
    "collectionName": "users",
    "email": "user@example.com",
    "verified": true
  },
  "meta": {
    "isNew": false
  }
}
```

## MFA Response

If MFA is required, auth endpoints return `401` with an MFA id instead of the normal auth response.

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `mfaId` | string | MFA flow id used by a second auth method. | `mfa_abc123` |

```json
{ "mfaId": "mfa_abc123" }
```

## GET /api/collections/{collection}/auth-methods

### Function

Returns enabled auth methods for an auth collection. Public endpoint.

### Path Parameters

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `collection` | string | Auth collection id or name. | `users` |

### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `password.enabled` | boolean | Whether password auth is enabled. | `true` |
| `password.identityFields` | array of strings | Fields accepted as identity values. | `["email", "username"]` |
| `oauth2.enabled` | boolean | Whether OAuth2 auth is enabled. | `true` |
| `oauth2.providers` | array | OAuth2 provider info. | `[]` |
| `oauth2.providers[].name` | string | Provider key. | `google` |
| `oauth2.providers[].displayName` | string | Human-readable provider name. | `Google` |
| `oauth2.providers[].state` | string | Random OAuth2 state. | `abc123` |
| `oauth2.providers[].authURL` | string | Auth URL with an empty `redirect_uri=` suffix. | `https://accounts.google.com/...&redirect_uri=` |
| `oauth2.providers[].codeVerifier` | string | PKCE verifier when provider supports PKCE. | `...` |
| `oauth2.providers[].codeChallenge` | string | PKCE challenge. | `...` |
| `oauth2.providers[].codeChallengeMethod` | string | PKCE challenge method. | `S256` |
| `mfa.enabled` | boolean | Whether MFA is enabled. | `false` |
| `mfa.duration` | integer | MFA duration in seconds. | `1800` |
| `otp.enabled` | boolean | Whether OTP is enabled. | `true` |
| `otp.duration` | integer | OTP validity in seconds. | `180` |

Legacy fields `authProviders`, `usernamePassword`, and `emailPassword` may also be returned.

### Response Example

```json
{
  "password": {
    "enabled": true,
    "identityFields": ["email"]
  },
  "oauth2": {
    "enabled": true,
    "providers": [
      {
        "name": "google",
        "displayName": "Google",
        "state": "state_123",
        "authURL": "https://accounts.google.com/o/oauth2/v2/auth?...&redirect_uri=",
        "authUrl": "https://accounts.google.com/o/oauth2/v2/auth?...&redirect_uri=",
        "codeVerifier": "",
        "codeChallenge": "",
        "codeChallengeMethod": ""
      }
    ]
  },
  "mfa": { "enabled": false, "duration": 0 },
  "otp": { "enabled": true, "duration": 180 }
}
```

## POST /api/collections/{collection}/auth-refresh

### Function

Refreshes or validates the current auth token for the same auth collection. Requires same auth collection token.

### Request

No body.

### Response

Shared auth response. If the token is marked refreshable, a new token is returned; otherwise the existing token is returned.

## POST /api/collections/{collection}/auth-with-password

### Function

Authenticates an auth record using password auth. Public endpoint.

### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `identity` | string | Yes | Email, username, or another configured identity field value. | `user@example.com` |
| `password` | string | Yes | Record password. | `secret123` |
| `identityField` | string | No | Explicit identity field to search. If omitted, configured identity fields are tried. | `email` |

### Request Example

```json
{
  "identity": "user@example.com",
  "password": "secret123",
  "identityField": "email"
}
```

### Response

Shared auth response or MFA response.

## POST /api/collections/{collection}/auth-with-oauth2

### Function

Authenticates or signs up a record using an OAuth2 provider. Public endpoint; a current auth token may be supplied for linking flows.

### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `provider` | string | Yes | OAuth2 provider name returned by `auth-methods`. | `google` |
| `code` | string | Yes | OAuth2 authorization code. | `4/0Ab...` |
| `codeVerifier` | string | No | PKCE verifier for PKCE providers. | `abc...` |
| `redirectURL` | string | Yes | Redirect URL used in the OAuth2 authorization flow. | `https://app.example.com/oauth2/callback` |
| `redirectUrl` | string | No | Legacy alias for `redirectURL`. | `https://app.example.com/oauth2/callback` |
| `createData` | object | No | Extra record fields to use when creating a new auth record. | `{ "name": "Alice" }` |

### Request Example

```json
{
  "provider": "google",
  "code": "4/0Ab...",
  "codeVerifier": "",
  "redirectURL": "https://app.example.com/oauth2/callback",
  "createData": {
    "name": "Alice"
  }
}
```

### Response

Shared auth response or MFA response. `meta` may include provider-specific user information and whether a new record was created.

## GET /api/oauth2-redirect

## POST /api/oauth2-redirect

### Function

Receives OAuth2 provider callbacks and forwards the callback data to a realtime client subscribed to `@oauth2`. The endpoint redirects to the bundled UI success/failure routes.

### Query or Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `state` | string | Yes | Realtime client id/state. Must refer to a client subscribed to `@oauth2`. | `client_123` |
| `code` | string | No | OAuth2 authorization code. Required for success. | `4/0Ab...` |
| `error` | string | No | OAuth2 error string. | `access_denied` |
| `user` | string | No | Apple-specific serialized user payload, accepted on POST form callback. | `{ "name": { "firstName": "A" } }` |

### Response

Redirects to one of:

| Result | Redirect Path |
| --- | --- |
| Success | `../_/#/auth/oauth2-redirect-success` |
| Failure | `../_/#/auth/oauth2-redirect-failure` |

## POST /api/collections/{collection}/request-otp

### Function

Creates and sends an OTP for an auth record email. Public endpoint.

### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `email` | string | Yes | Auth record email. | `user@example.com` |

### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `otpId` | string | OTP id used with `auth-with-otp`. | `otp_123` |
| `mfaId` | string | Optional MFA id when OTP is used as part of MFA. | `mfa_123` |

### Response Example

```json
{ "otpId": "otp_123" }
```

## POST /api/collections/{collection}/auth-with-otp

### Function

Authenticates with an OTP id and OTP password/code. Public endpoint.

### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `otpId` | string | Yes | OTP id returned by `request-otp`. | `otp_123` |
| `password` | string | Yes | OTP code sent by email. | `123456` |

### Response

Shared auth response or MFA response.

## Custom Token Binding

### POST /api/collections/{collection}/bind-token

#### Function

Binds a custom token to an auth record after verifying email and password. Public endpoint.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `email` | string | Yes | Auth record email. | `user@example.com` |
| `password` | string | Yes | Auth record password. | `secret123` |
| `token` | string | Yes | Custom token to bind. | `device-token-1` |

#### Response

`204 No Content`.

### POST /api/collections/{collection}/unbind-token

#### Function

Removes a custom token binding after verifying email and password.

#### Request Body Fields

Same as `bind-token`.

#### Response

`204 No Content`.

### POST /api/collections/{collection}/auth-with-token

#### Function

Authenticates using a previously bound custom token.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `token` | string | Yes | Bound custom token. | `device-token-1` |

#### Response

Shared auth response or MFA response.

## Password Reset

### POST /api/collections/{collection}/request-password-reset

#### Function

Sends a password reset email. Public endpoint.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `email` | string | Yes | Auth record email. | `user@example.com` |

#### Response

`204 No Content`.

### POST /api/collections/{collection}/confirm-password-reset

#### Function

Confirms a password reset token and sets a new password.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `token` | string | Yes | Password reset token. | `eyJ...` |
| `password` | string | Yes | New password. | `newSecret123` |
| `passwordConfirm` | string | Yes | Must match `password`. | `newSecret123` |

#### Response

`204 No Content`.

## Verification

### POST /api/collections/{collection}/request-verification

#### Function

Sends a verification email. Public endpoint.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `email` | string | Yes | Auth record email. | `user@example.com` |

#### Response

`204 No Content`.

### POST /api/collections/{collection}/confirm-verification

#### Function

Confirms an email verification token.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `token` | string | Yes | Verification token. | `eyJ...` |

#### Response

`204 No Content`.

## Email Change

### POST /api/collections/{collection}/request-email-change

#### Function

Requests an email change for the current auth record. Requires same auth collection token.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `newEmail` | string | Yes | New email address. | `new@example.com` |

#### Response

`204 No Content`.

### POST /api/collections/{collection}/confirm-email-change

#### Function

Confirms an email change token and verifies the current password.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `token` | string | Yes | Email change token. | `eyJ...` |
| `password` | string | Yes | Current password for the auth record. | `secret123` |

#### Response

`204 No Content`.

## POST /api/collections/{collection}/impersonate/{id}

### Function

Creates an auth token for another auth record. Requires superuser authentication.

### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `duration` | integer | No | Token duration in seconds. If omitted, collection auth token duration applies. | `3600` |

### Response

Shared auth response for the target auth record.

## POST /api/files/token

### Function

Creates a short-lived file token for the currently authenticated record. Requires any valid auth token.

### Request

No body.

### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `token` | string | File token for protected file downloads. | `eyJ...` |

### Response Example

```json
{ "token": "eyJhbGciOi..." }
```

## GET /api/files/{collection}/{recordId}/{filename}

### Function

Downloads a stored record file. Access is public only if collection file/view rules allow it; otherwise a valid auth/file token is required.

### Path Parameters

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `collection` | string | Collection id or name. | `posts` |
| `recordId` | string | Record id. | `post_1` |
| `filename` | string | Stored file name. | `photo_abcd.jpg` |

### Query Parameters

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `token` | string | No | Auth or file token. | `eyJ...` |
| `thumb` | string | No | Thumbnail size for supported image fields. | `100x100` |

### Response

Binary file response. Content type is determined by the stored file/thumbnail.
