### Security
- Cross-site browser write requests carrying a session cookie and GraphQL mutations sent over GET are now refused. GraphQL queries also enforce depth, field and alias limits.
- OIDC login initiation now caps pending logins and rate-limits repeated requests, and camera and event adapters no longer follow HTTP redirects. The Kubernetes manifest disables automatic service account token mounting.
