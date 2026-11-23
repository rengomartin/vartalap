# Third-party notices

Vartalap vendors the following assets so the binary has no runtime CDN
dependency. All application cryptography uses the browser's built-in WebCrypto
API; no third-party cryptography library is bundled or required.

## IBM Carbon Design System

- Files: `static/css/carbon.css`
- Source: <https://github.com/carbon-design-system/carbon>
- License: Apache License 2.0
- Copyright: IBM Corp.

## IBM Plex fonts

- Files: `static/fonts/*.woff2` (Latin1 subsets of IBM Plex Sans and IBM Plex Mono)
- Source: <https://github.com/IBM/plex>
- License: SIL Open Font License 1.1
- Copyright: IBM Corp.

## HTMX

- Files: `static/js/htmx.min.js`
- Source: <https://github.com/bigskysoftware/htmx>
- License: BSD 2-Clause

## BIP-39 English wordlist

- Files: `static/js/bip39-wordlist.js`
- Source: <https://github.com/bitcoin/bips/blob/master/bip-0039/english.txt>
- License: BSD 2-Clause (BIPs repository); the wordlist is widely treated as
  public-domain reference data.

## Go modules

- `github.com/go-chi/chi/v5` — MIT
- `github.com/gorilla/websocket` — BSD 3-Clause
- `github.com/mattn/go-sqlite3` — MIT
