### Added
- [sdk] A module's printer tab may carry `cross_origin_isolated: true` in its manifest, and the server then sends that tab's page with `Cross-Origin-Opener-Policy: same-origin` and `Cross-Origin-Embedder-Policy: require-corp`. A device's own page framed there may then use `SharedArrayBuffer`, and the web app reloads the page when a person opens or leaves the tab.
