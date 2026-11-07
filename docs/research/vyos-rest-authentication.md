# VyOS REST authentication implementation verification

Verified 2026-11-06 against upstream public `rolling` commit
`d6902e6423ec78b5b99d96b6427ebee771ea6c38`. This is source inspection, not an execution
against a router. No live router configuration was changed.

## Actual wire protocol

- `POST /token` accepts form `key=<API key>` (multipart or URL encoded) or JSON
  `{"key":"..."}`. JSON `{}` with `X-API-Key` also satisfies the endpoint.
  A form without `key` is rejected before endpoint dispatch, even with that
  header. See [request conversion and token route](https://github.com/vyos/vyos-1x/blob/d6902e6423ec78b5b99d96b6427ebee771ea6c38/src/services/api/rest/routers.py).
- Successful response is `{"success":true,"data":{"token":"...","expires_in":3600},"error":null}`,
  **not** an OAuth `access_token` response. `expires_in` is seconds, defaults
  to 3600. The JWT is HS256 with `iss=vyos-rest-api`, `sub=<API key ID>`, and
  `exp`; the signing secret is generated in memory. See [token generation](https://github.com/vyos/vyos-1x/blob/d6902e6423ec78b5b99d96b6427ebee771ea6c38/src/services/api/rest/libs/token_auth.py)
  and [response envelope](https://github.com/vyos/vyos-1x/blob/d6902e6423ec78b5b99d96b6427ebee771ea6c38/src/services/api/rest/models.py).
- `/token` is behind the shared authentication dependency and additionally
  checks that an API key was supplied. A bearer token or mTLS certificate
  alone cannot mint a token. There is no refresh token; refresh by repeating
  the key exchange. [Route implementation](https://github.com/vyos/vyos-1x/blob/d6902e6423ec78b5b99d96b6427ebee771ea6c38/src/services/api/rest/routers.py).
- Shared REST auth order: nginx-verified mTLS, bearer token (local JWT then
  OIDC), body key or `X-API-Key`. A supplied invalid bearer token produces
  HTTP 401 instead of falling back to a supplied key. Every route on the
  authenticated router uses the dependency, including configure, retrieve,
  config-file, show, renew, etc. [Shared dependency and router](https://github.com/vyos/vyos-1x/blob/d6902e6423ec78b5b99d96b6427ebee771ea6c38/src/services/api/rest/routers.py).

## Expiration and retries

- Expiry is generated using server UTC; PyJWT encodes datetime expiry as
  integer epoch seconds. A client should base its relative expiry on the
  time the exchange request starts and use a safety margin (covers network
  delay and integer-second truncation). This is an implementation inference
  from the token-generation code above.
- The signing secret belongs to a process-local singleton, so a full API
  process restart invalidates cached tokens even before their expiry. An
  in-process config reload keeps the existing secret. See [session state](https://github.com/vyos/vyos-1x/blob/d6902e6423ec78b5b99d96b6427ebee771ea6c38/src/services/api/session.py)
  and [server reload/initialization](https://github.com/vyos/vyos-1x/blob/d6902e6423ec78b5b99d96b6427ebee771ea6c38/src/services/vyos-http-api-server).
- Provider policy: clear the cached token on HTTP 401 for the next operation,
  and fail the current operation. Never replay requests, including mutations,
  after authentication, network or server errors.

## External bearer tokens / OIDC

The client only needs to send `Authorization: Bearer <externally obtained
token>`. Identity-provider issuer, JWKS URL and audience belong on the router,
not the Terraform client. Current server checks `exp`, issuer and audience;
supports RS256/384/512 and ES256/384/512; accepts `sub` or `client_id` as caller
identity; and has 30-second leeway. JWKS can be explicit or discovered from
`<issuer>/.well-known/openid-configuration`. See [OIDC verifier](https://github.com/vyos/vyos-1x/blob/d6902e6423ec78b5b99d96b6427ebee771ea6c38/src/services/api/rest/libs/token_auth.py).

Caller-supplied tokens are opaque credentials to the provider. There is no
evidence for a provider-side interactive identity-provider flow or refresh
credential protocol. OIDC audience fail-closed fix is
[`f75c7b74dc4c2f38ad9e61e08dfe9fd1fc0686f9`](https://github.com/vyos/vyos-1x/commit/f75c7b74dc4c2f38ad9e61e08dfe9fd1fc0686f9)
(2026-09-21).

## Client certificates and server TLS

The server's nginx verifies the client certificate against the configured
selected CA. It sends its own `X-Client-Verify` value to the application;
`SUCCESS` permits authentication without any key/token. The provider must
present the actual TLS client certificate and key and must never synthesize
that header. See [nginx template](https://github.com/vyos/vyos-1x/blob/d6902e6423ec78b5b99d96b6427ebee771ea6c38/data/templates/https/nginx.default.j2)
and [CA handling](https://github.com/vyos/vyos-1x/blob/d6902e6423ec78b5b99d96b6427ebee771ea6c38/src/conf_mode/service_https.py).

The [mTLS introduction commit](https://github.com/vyos/vyos-1x/commit/0cdf7e696b4deb3c3103a596bfe4d4f167dcf4b7)
explicitly documents that nginx 1.22 TLS 1.3 post-handshake authentication
limitations require TLS 1.2. The [September announcement](https://blog.vyos.io/vyos-project-september-2026-update)
confirms TLS 1.2 only. Setting only a minimum TLS version does not enforce
this; the client should cap mTLS connections at TLS 1.2 as well. Server CA
verification remains independently necessary; client credentials do not
authenticate the server.

The [header passthrough fix](https://github.com/vyos/vyos-1x/commit/fa46c294c8239534472a140293125864c0ea857d)
(2026-09-11, announcement describes fixed rolling builds from 2026-09-12)
makes the nginx overwrite unconditional. Before this, clients could supply
the header when mTLS was disabled. The announcement identifies affected
rolling builds as 2026-08-26 through 2026-09-12 and states no LTS release
carried this mTLS code. A further [CA trust boundary fix](https://github.com/vyos/vyos-1x/commit/71ea2f3d9f654f1b3ab9e08fa6cdff4447b3fc0a)
scoped trust to the selected CA (2026-09-02).

## Version support

The changes are present in current rolling source, introduced by
[`f3012e652edef614d4f0ed169b320106a85d83b3`](https://github.com/vyos/vyos-1x/commit/f3012e652edef614d4f0ed169b320106a85d83b3)
(bearer/exchange),
[`0cdf7e696b4deb3c3103a596bfe4d4f167dcf4b7`](https://github.com/vyos/vyos-1x/commit/0cdf7e696b4deb3c3103a596bfe4d4f167dcf4b7)
(mTLS), and
[`b8136290e326ba8a1879a3c55eea9e0a1303fedd`](https://github.com/vyos/vyos-1x/commit/b8136290e326ba8a1879a3c55eea9e0a1303fedd)
(OIDC). Author dates precede the merge/deployment period; use the September
announcement for the shipping period rather than claiming a particular
rolling image from author date alone.

Public release-branch inspection found none of `/token`, bearer REST, OIDC
REST or mTLS configuration in their API server/nginx/conf-mode sources:

- [circinus-public-unmaintained at b9b966a1284a8a7ca355a63b4fbaa6a6b9d7110c](https://github.com/vyos/vyos-1x/tree/b9b966a1284a8a7ca355a63b4fbaa6a6b9d7110c)
  (1.5 line).
- [sagitta-public-unmaintained at 64fba5e6ac5a9454bcae319aa3cfdc8fab8299c6](https://github.com/vyos/vyos-1x/tree/64fba5e6ac5a9454bcae319aa3cfdc8fab8299c6)
  (1.4 line).

This does not establish the status of privately maintained LTS branches or
future backports. Document these methods as requiring a supporting VyOS
build, with current rolling verified, and retain API-key mode for older
servers. An explicitly requested exchange must fail clearly on unsupported
`/token`, rather than silently downgrade to long-lived key authentication.
