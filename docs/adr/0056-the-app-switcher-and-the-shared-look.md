---
status: proposed
---

# 56. The app switcher and the shared look

Date: 2026-11-28

Part of M7, the combined deployment with PagerArchive ([issue #128](https://github.com/RIKIKU/RadioArchive/issues/128)). PagerArchive v1.25.0 put ArchiveUx's app switcher to the radio in its top bar, and shares the look in a `look` cookie on the parent domain (its R77e, R92b and ADR 0052). The owner, trying it on 2026-11-28: "The theme isn't remembered from one to the other. RadioArchive doesn't have the switch button." This is the radio's half: R102's switcher and R104's shared look, built on ArchiveUx's `Frame`, `AppSwitcher` and `look.js` (its ADR 0002, decisions 1, 4, 9 and 11). The owner then decided the light or dark theme is shared too (2026-11-28, "Light/dark should also be shared"): ArchiveUx 0.7.0 adds a `theme` cookie of its own beside the unchanged `look` cookie (its rule 8: `themeFromCookies`, `pickTheme`, `themeCookie`, `applyTheme`, and `AccountMenu`'s `ontheme`), and the radio moves to it.

## Summary

| Part | Choice |
|---|---|
| **Settings** | `PAGERARCHIVE_URL` (an absolute http(s) URL, no user name, query or fragment, trailing `/` dropped) turns on the switcher; `PAGERARCHIVE_NAME` names it, `Pager` unless set, as PagerArchive's `RADIOARCHIVE_NAME` defaults to `Radio`. `LOOK_COOKIE_DOMAIN` (lower-cased, a leading dot dropped) shares the look; it needs `APP_URL`, whose host must be in it. A bad value stops the server at start, naming the setting. All unset is standalone, exactly as before |
| **Who sees the switcher** | Everyone who can reach the radio. While the radio is in preview, that's the preview group, behind the sign-in gate in front of it, which is also the group PagerArchive shows its switcher to. The radio adds no access control of its own |
| **How the browser learns them** | On the page's root element, written by the server with the look: `data-pagerarchive-name` and `data-pagerarchive-url`, and `data-look-shared="<domain>"` when the look is shared for that request. No endpoint and no wait: the frame draws the switcher in the first paint. The values are escaped; the page carries no deployment data beyond them |
| **The switcher** | ArchiveUx's, through `Frame`'s `otherApp`: the top bar, before the account menu, on desktops and phones, "Pager" with the exchange icon, named "Switch to Pager", opening PagerArchive in the same tab. Left out of a printout |
| **The cookie** | ArchiveUx's `lookCookie` exactly, as PagerArchive writes it: `look=<harbour or console>; Domain=<LOOK_COOKIE_DOMAIN>; Path=/; Max-Age=31536000; SameSite=Lax`, and `Secure` on HTTPS. Not `HttpOnly`. The value is the look's name only; the light or dark theme isn't in it |
| **First paint** | Shared, the server reads the first `look` cookie as `look.js`'s `lookFromCookies` does (as is or in double quotes, an unknown look is none) and sends the page in it, marked `data-look-shared`, as PagerArchive's `<html data-look-shared data-look="console">` |
| **The radio's own cookie** | Standalone, the radio keeps the look in a host-only cookie of the same name, `look`. Shared, the page sent in a look ends that host-only cookie and writes the look to the shared one (two `Set-Cookie` headers), so the browser never holds two `look` cookies whose order decides the look. A look chosen here before the setting is carried into the shared cookie by the first page this way |
| **A change** | The account menu's choice applies at once and is written by the page: the host-only cookie ended, then the shared one written with `lookCookie`. Signed in, it's saved to the account too, as before |
| **A signed-in account** | The look the account's profile comes in with is the shared cookie's when the cookie holds one, else the account's, and then written to the cookie. When the two differ the account is saved with the cookie's. So a look chosen in PagerArchive since is kept, and the page never switches looks once the account loads, as in PagerArchive |
| **Other hosts** | The look is shared only for a page asked for on the domain or under it. On another host (the server's own address, say) the browser would refuse the cookie, so the look stays that host's own there, as standalone |
| **The theme** | Shared the same way, in a `theme` cookie of its own (ArchiveUx 0.7.0). `light` or `dark` with the look cookie's attributes; Auto deletes it (`Max-Age=0`, same Domain and Path), so absent follows the system. First paint: the first `theme` cookie holding light or dark, as `themeFromCookies` reads it (an empty or unknown one is passed over, unlike the look's), else the system's. The page sent in light or dark moves the host-only `theme` cookie into the shared one, as for the look. A change goes through `AccountMenu`'s `ontheme` to `applyTheme`, which ends the host-only cookie first. A signed-in account's theme gives way to the cookie's, as its look does. Standalone, the `theme` cookie stays host-only, as before, written by `applyTheme` with no domain |

## Considered

- **The server writing the cookie, the page never writing `document.cookie`** (PagerArchive's way, ADR 0052 there). PagerArchive's look changes go to its API, whose answer sets the cookie. The radio's look works signed out, with no request to answer, so its page writes the cookie as it always has, with ArchiveUx's `lookCookie`. Only the move of the host-only cookie is the server's, because it has to happen before the first paint reads the cookies.
- **The account's look at sign-in, every time** (R104 as first written: "copied into it at sign-in"). The radio signs in silently on every browser session's first visit, and its account isn't told of a change made in PagerArchive, so copying the account's look each time would undo the other app's choice at every new session: the owner's report. The cookie wins instead, and the account follows it; an account's look is copied into the cookie only when there is no shared look yet.
- **A config endpoint.** The frame would draw without the switcher, then with it once the answer came. The root element is already where the server puts the look.
- **The last of two `look` cookies.** A browser sends cookies of the same path oldest first, so with the radio's old host-only cookie beside a shared one, either could be first. Ending the host-only one on the first page leaves one cookie, read as `look.js` reads it.

## Consequences

- The radio's `web` package takes a `Combined` (the settings above) and still reads nothing from the database.
- A browser with the radio's look from before `LOOK_COOKIE_DOMAIN` was set, and a different look chosen in PagerArchive since, sees whichever cookie it sends first on its first radio page after the change; from then on, one cookie.
- Auto is the absence of a `theme` cookie, so a radio account holding Dark, signed in again in a browser whose shared theme was set to Auto in PagerArchive, writes Dark back: the cookie can't say "Auto chosen" apart from "nothing chosen".
- The browser tests reach a binary of their own as `radio.example.test`, with it as their HTTP proxy, since a domain cookie needs a domain name (`scripts/ci/e2e-web.sh`).
