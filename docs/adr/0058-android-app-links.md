---
status: proposed
---

# 58. Android App Links

Date: 2026-11-28

The owner, 2026-11-28, wants PagerArchive's radio links (R101, [ADR 0057](0057-several-fireground-channels-in-one-link.md)) to open the RadioArchive Android app when it's installed. Android opens a host's `https` links in an app only when the host lists the app, by its package name and signing certificate, in `https://<host>/.well-known/assetlinks.json` ([Android's App Links verification](https://developer.android.com/training/app-links/verify-android-applinks)). A50 checks it.

## Summary

| Part | Choice |
|---|---|
| **The setting** | `ANDROID_APP_LINKS`: items separated by commas or semicolons, each `package:fingerprint`, or a fingerprint alone, another for the package before it. A fingerprint is a SHA-256 certificate fingerprint as keytool and Play Console show it, 32 hex pairs separated by colons; it's written in upper case. A package is two or more dot-separated parts, each starting with a letter. Duplicates are dropped. A value that isn't one stops the server at start, naming the setting |
| **The file** | `GET /.well-known/assetlinks.json`: `[{"relation":["delegate_permission/common.handle_all_urls"],"target":{"namespace":"android_app","package_name":"…","sha256_cert_fingerprints":["…"]}}]`, one statement per package, in the setting's order. `Content-Type: application/json`, `Cache-Control: public, max-age=3600`, never a redirect |
| **Unset** | `404`, not the app's page, which would otherwise answer the path as it does every path outside the API |
| **Where** | An app route of the `web` package, beside `/robots.txt`: a deployment setting, never data, so it carries nothing the gate guards. The A10 harness probes it |
| **The gate** | The path must be reachable without any sign-in gate: Android fetches it anonymously and treats a redirect as a failure (`docs/deploy.md`) |

## Considered

- **The fingerprint built in.** The upload key's fingerprint is known now, but Play App Signing will add another, and a fork's app has its own. A setting changes without a release.
- **A file mounted into the container.** One more volume and a file to keep in step; the content is a package name and a fingerprint or two, which fit one setting.
- **Longer caching.** Android reads the file at install and from time to time after; an hour lets a corrected fingerprint reach a re-verification soon, at no real cost.

## Consequences

- The Android app declares `autoVerify` intent filters for the radio's host and the paths it handles (`/` with `fireground`, and others it wants), in its own repository.
- When Play App Signing is turned on, its certificate's fingerprint is added to `ANDROID_APP_LINKS` after a semicolon, beside the upload key's.
- A deployment behind a sign-in gate exempts exactly `/.well-known/assetlinks.json`.
