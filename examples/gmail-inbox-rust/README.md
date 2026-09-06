# Gmail inbox (Rust)

A small mail client. Same behavior as the Python and Node apps: profile, then
the latest messages. The HTTP client is a few dozen lines on `native-tls` so
the example still builds on Rust 1.83.

## Build

```bash
cargo build
```

## Run it

```bash
fab run acme-gmail -- ./target/debug/gmail-inbox
```

From the repository root:

```bash
cargo build --manifest-path examples/gmail-inbox-rust/Cargo.toml
./scripts/fab-dev run acme-gmail -- examples/gmail-inbox-rust/target/debug/gmail-inbox
```

Search:

```bash
fab run acme-gmail -- ./target/debug/gmail-inbox INV-4812
```

## Keep Gmail's real hostname

The app honors `HTTPS_PROXY` with an HTTP `CONNECT` tunnel and loads
`SSL_CERT_FILE` so the Fabricate CA is trusted:

```bash
fab run acme-gmail --proxy -- ./target/debug/gmail-inbox
```
