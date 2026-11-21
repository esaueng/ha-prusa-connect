### Security
- Cross-site browser write requests carrying a session cookie and GraphQL mutations sent over GET are now refused. GraphQL queries also enforce depth, field and alias limits.
- OIDC login initiation now caps pending logins and rate-limits repeated requests, and camera and event adapters no longer follow HTTP redirects. The Kubernetes manifest disables automatic service account token mounting.
- The web app now sends framing and referrer headers, module routes require authentication before lookup, and installing a module from a custom source requires the organization admin role. Revoking another user's API credential without admin access now returns the same 404 as an unknown credential.
