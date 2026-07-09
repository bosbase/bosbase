# Health, Settings, and Admin Utilities

This document covers administrative HTTP APIs that do not belong to collection data access.

## GET /api/health

### Function

Checks whether the API is healthy. The endpoint is public. If a valid superuser token is supplied, the response includes extra operational diagnostics.

### Request

No body.

### Query Parameters

None.

### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `code` | integer | HTTP status code mirrored in the payload. | `200` |
| `message` | string | Health message. | `API is healthy.` |
| `data` | object | Diagnostic data. Public requests receive `{}`. Superusers receive extra fields. | `{}` |
| `data.canBackup` | boolean | Superuser-only. Whether a backup can be started now. | `true` |
| `data.realIP` | string | Superuser-only. Client IP after trusted proxy resolution. | `203.0.113.10` |
| `data.requireS3` | boolean | Superuser-only. Whether S3 is required by app settings/runtime. | `false` |
| `data.possibleProxyHeader` | string | Superuser-only. Header that suggests a reverse proxy is present. | `X-Forwarded-For` |

### Response Example

```json
{
  "code": 200,
  "message": "API is healthy.",
  "data": {}
}
```

## GET /api/settings

### Function

Returns the current application settings. Requires superuser authentication.

### Request

No body.

### Response

Returns the complete settings object. The exact shape follows the `core.Settings` model and includes groups such as meta, logs, SMTP, S3, backups, rate limits, trusted proxy, and auth provider configuration.

### Response Example

```json
{
  "meta": {
    "appName": "BosBase",
    "appURL": "http://127.0.0.1:8090"
  },
  "logs": {
    "maxDays": 30,
    "logAuthId": false
  },
  "rateLimits": {
    "enabled": false,
    "rules": []
  }
}
```

## PATCH /api/settings

### Function

Updates application settings. Requires superuser authentication. The submitted body is bound on top of the current settings object and then saved.

### Request Body Fields

Because the endpoint accepts the full settings model, fields are grouped by settings section. Only include fields you intend to update.

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `meta` | object | No | App metadata such as name and URL. | `{ "appName": "My App" }` |
| `logs` | object | No | Log retention and logging options. | `{ "maxDays": 14 }` |
| `smtp` | object | No | SMTP email delivery settings. | `{ "enabled": true, "host": "smtp.example.com" }` |
| `s3` | object | No | File storage S3 settings. | `{ "enabled": true, "bucket": "files" }` |
| `backups` | object | No | Backup settings and backup S3 settings. | `{ "cron": "0 2 * * *" }` |
| `rateLimits` | object | No | Rate limit settings. | `{ "enabled": true, "rules": [] }` |
| `trustedProxy` | object | No | Trusted proxy header configuration. | `{ "headers": ["X-Forwarded-For"] }` |

### Request Example

```json
{
  "meta": {
    "appName": "Production BosBase",
    "appURL": "https://api.example.com"
  },
  "rateLimits": {
    "enabled": true,
    "rules": [
      {
        "label": "POST /api/collections/users/auth-with-password",
        "audience": "@guest",
        "duration": 60,
        "maxRequests": 5
      }
    ]
  }
}
```

### Response Fields

Returns the updated settings object. Field meanings are the same as `GET /api/settings`.

### Response Example

```json
{
  "meta": {
    "appName": "Production BosBase",
    "appURL": "https://api.example.com"
  },
  "rateLimits": {
    "enabled": true,
    "rules": [
      {
        "label": "POST /api/collections/users/auth-with-password",
        "audience": "@guest",
        "duration": 60,
        "maxRequests": 5
      }
    ]
  }
}
```

## POST /api/settings/test/s3

### Function

Tests the configured S3 filesystem by uploading and deleting a temporary object. Requires superuser authentication.

### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `filesystem` | string | Yes | Which configured S3 filesystem to test. Allowed values: `storage`, `backups`. | `storage` |

### Request Example

```json
{
  "filesystem": "storage"
}
```

### Response

`204 No Content` on success.

## POST /api/settings/test/email

### Function

Sends a test email for one of the configured auth email templates. Requires superuser authentication.

### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `email` | string | Yes | Recipient email address. | `admin@example.com` |
| `template` | string | Yes | Template to test. Allowed values: `verification`, `password-reset`, `email-change`, `otp`, `login-alert`. | `verification` |
| `collection` | string | No | Auth collection id or name. Defaults to `_superusers`. | `users` |

### Request Example

```json
{
  "email": "admin@example.com",
  "template": "verification",
  "collection": "users"
}
```

### Response

`204 No Content` on success.

## POST /api/settings/apple/generate-client-secret

### Function

Generates a Sign in with Apple client secret JWT. Requires superuser authentication.

### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `clientId` | string | Yes | Apple Service ID / app identifier. | `com.example.web` |
| `teamId` | string | Yes | Apple developer team id. Must be 10 characters. | `A1B2C3D4E5` |
| `keyId` | string | Yes | Apple private key id. Must be 10 characters. | `K1L2M3N4O5` |
| `privateKey` | string | Yes | EC private key PEM text. | `-----BEGIN PRIVATE KEY-----...` |
| `duration` | integer | Yes | JWT validity in seconds. Max `15777000`. | `86400` |

### Request Example

```json
{
  "clientId": "com.example.web",
  "teamId": "A1B2C3D4E5",
  "keyId": "K1L2M3N4O5",
  "privateKey": "-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----",
  "duration": 86400
}
```

### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `secret` | string | Generated Apple client secret JWT. | `eyJhbGciOiJFUzI1NiIsImtpZCI6...` |

### Response Example

```json
{
  "secret": "eyJhbGciOiJFUzI1NiIsImtpZCI6IksxTDJNM040TzUifQ..."
}
```

## GET /_/{path...}

### Function

Serves bundled admin UI static assets. This is not a data API, but it is registered by the Go server.

### Request

No API request body.

### Response

Static file response with asset content type.
