### Security
- Cross-site browser write requests carrying a session cookie and GraphQL mutations sent over GET are now refused. GraphQL queries also enforce depth, field and alias limits.
