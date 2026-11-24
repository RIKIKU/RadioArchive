---
status: proposed
---

# 17. Call links: the link's path, the page route and the preview

Date: 2026-11-25

A call's link is `/calls/<call ID>`, the call's own UUID as the API gives it. Its page is the app's page with the call's preview tags, served by a new kind of route in `server.API`'s table, the **page route**, which lives outside the API like the app's routes but can only come from `internal/public`, reads through the gate, and sees no cookie or credential. Every unavailable call's page is the app's own page, byte for byte, with `404`. The browser renders the call card from the call details. **Share** copies the link, from a recent-calls row, the call card and now playing. These are the implementer's choices where `SPEC.md` P1, R23, R24, R32, R33, R45-R47, R60-R62, R74 and R88 leave room (spec "How to use this spec", item 6). Built in [Call links: the call card, its preview and Share](https://github.com/RIKIKU/RadioArchive/issues/80).

## Summary

| Part | Choice |
|---|---|
| **The link** | `/calls/{call}`: the call's ID as the API writes it (a UUID, 36 characters, R60). Case doesn't matter. Nothing else in it |
| **Route kind** | `server.PageRoute`: a path outside `/api`, the sign-in hops and `/healthz`, never `/` itself. Mounted only by `server.WithPublic` from `public.API.Pages()`. Its handler gets the request without `Cookie` or `Authorization`, as a public route does |
| **The page** | `web.App.Page`: the app's `index.html` in the asker's look and theme (their cookies, read by the app, which holds no data), with the preview's tags at the end of `<head>`. `Cache-Control: no-store`, `Vary: Cookie`, the app's CSP; `X-Robots-Tag: noindex` as every response (R74) |
| **The preview** | `og:type` `website`, `og:site_name` `RadioArchive`, `og:title` the talkgroup's name (R24), `og:description` and `description` the call's start on the deployment's clock (`Wed 25 Nov 2026, 14:03:22 AEDT`), `twitter:card` `summary`. HTML-escaped. No audio, no recording or download URL, no units, no call ID |
| **Unavailable** | Deleted, never stored, hidden, ignored, patched with either, within its delay (R23), not a call ID: the gate gives no call (`Gate.Calls`), and the page is the app's own page, `404`, the same bytes and headers whatever the reason. The site's generic preview is that page: its `<title>RadioArchive</title>`, no preview tags |
| **Failure** | The gate's error: the app's own page with `500`, logged without the call's ID |
| **The card** | `web/src/routes/calls/[id]/+page.svelte`, from `POST /api/v1/calls` (ADR 0004): the talkgroup's name and groups, the start in full on the deployment's clock, the duration, the units heard (radio IDs, #83 adds aliases), **Play** (replay, ADR 0016), **Download** (ADR 0014), **Share**. Live listening doesn't start; **Start listening** stays in the dock |
| **In progress** | "In progress" and **Listen live**, which adds the talkgroup for the session if it isn't in the set (R47, ADR 0008) and starts or resumes listening. The page asks for the call again every **5 s** while it has no recording, so the same open card turns into Play once the recording arrives |
| **Unavailable, in the page** | "This call isn't available", with links to Recent calls and Listen: one page, no reason, nothing about the talkgroup |
| **Below the card** | ADR 0016's `RecentList`, searching the call's talkgroup from the call's start, oldest first, so the list opens at the call, marked "This call"; with a link to all of the talkgroup's recent calls |
| **Share** | `web/src/lib/share/ShareButton.svelte`: copies `<origin>/calls/<id>` with the Clipboard API, says "Link copied" (and to a screen reader); where the browser won't copy, shows the link in a field, selected. On every recent-calls row (through ADR 0016's `actions`), on the card, and in the dock for the live call playing and for a replay |

## Why the call's UUID in the path

R60 asks only that IDs be opaque and non-sequential, which the UUID is. The API, the live feed, recordings and downloads all name a call by it, so the link is the same string a listener (or a support question) sees anywhere else, and the page passes it straight to the call details. #75's note on 22-character base64url IDs is about fitting many calls into a zip's URL (ADR 0015); one call in a link has no such budget. A path, not a query (`/?call=`), because the page differs by call and so needs a route of its own, and a path is what a person expects to paste.

## Why a page route

Until now the app shell was the same page for every path outside the API, and the A10 harness checked that nothing depended on the call a path names. A link preview (R61, R74) can only come from the page's HTML: preview fetchers don't run the app. So the page now depends on the call, and that dependence has to go through the gate like any other public read (R32). The options:

- **A public route outside `/api`.** The public kind's rule is "under `/api`", which keeps the app shell from ever answering for the API and vice versa. Relaxing it for one pattern weakens a check the route table makes for every route.
- **Let the app shell read the call.** The `web` package carries no deployment data and imports nothing of the repo (its test enforces it). Giving it the gate would undo ADR 0004's separation for the one page that most needs it.
- **A route kind of its own** (chosen). `PageRoute` sits where app routes do, but only `WithPublic` mounts it, from `public.API.Pages()`, so a page that reads data can only come from the package that reads through the gate. The page's data half (`public.Page.Preview`) sees the request anonymised, so signing in can't change it (P3, ADR 0009); the page's look half is the app's, which knows the listener's look and theme cookies and nothing about the deployment. The server joins them.

The A10 harness probes page routes as it probes public routes (it fails while one is mounted without probes), and `TestIntegrationSignedInSameAnswers` replays them with a session added to the probe's own cookies. The app shell's probes for "a call's path" now probe paths that name a call but aren't its link (`/calls/<id>/more`, `/recent?call=<id>`), which must still not depend on it.

## Unavailable calls: the app's own page, 404

R33 and R61: "the same status, same page, no reason, nothing naming the talkgroup", and an unavailable call gets "the site's generic preview". The simplest page that's the same for every reason is the one the site already sends everywhere else, so an unavailable call's link answers exactly what `/` does, but `404`. The app then renders "This call isn't available" from the call details' empty answer, which is the same for every reason (ADR 0004).

`404` rather than `200`: a link to something that isn't there says so to a preview fetcher, a crawler that ignores `noindex`, and a script checking links, and it's the status ADR 0004's API answers for the same call. The browser renders a `404` page's HTML as any other, so the app runs as usual.

## Calls in progress

A16: the card says "In progress" and offers live listening; "after it ends the same link plays the recording". Reloading the link does that by itself, since the page reads the call details each time. An open card asks again every 5 s while the call has no recording (in progress, or ended with the upload still on its way), and stops once it has one or isn't available any more. The live feed's events would say sooner, but only for a talkgroup the listener hears, and a card for a talkgroup they don't follow would have to subscribe to it; polling one indexed lookup every 5 s per open card is nothing at the deployment's scale (section 2).

Listen live adds the talkgroup for the session (R47), shown on Listen as "added for this session" with **Keep**, as a fireground link's is, and starts listening (or resumes it if paused). A talkgroup already in the set is left as it is. A delayed talkgroup's call is never in progress publicly (R23): it's unavailable until its delay has passed since it ended.

## Left for later

- **Play from here** under the card is #92's, blocked on #75's open question 3. The list below the card is `RecentList`, where #92 adds it to every row.
- **Unit aliases** on the card and its list are #83's (R27). The card shows radio IDs; `unitsOf` is the one place that turns transmissions into the units shown.
- **Add to basket** on the card and in the dock is #81's.

## Open points for the owner

1. **The generic preview.** It's the app's own page: previews show "RadioArchive" and nothing else. A site description (say, "Live and recorded radio") could go in `app.html`, for every page and every unavailable link alike; say if you want one, and its words.
2. **What the preview says.** The talkgroup's name, and the start with the zone's abbreviation. No group, duration or "in progress": previews are cached by messaging apps, and "in progress" would stay in the preview after the call ends.
3. **Below the card.** "Scrolled to that time" is read as the list starting at the call, oldest first (ADR 0016's consequence): what happened next. Calls before it are a link away (all recent calls on the talkgroup). If you'd rather see a few calls before it too, the list would load the page before and scroll to the call.

## How each choice is verified

| Choice | Verification |
|---|---|
| The page route's place in the table | `TestRouteTableRefusesMisplacedRoutes`; the binary serves the public API's pages and no others (`TestIntegrationNoAdminAPI` in `cmd/radioarchive`) |
| A10: every unavailable call's link equals the deleted call's, in every look, with a query, in capitals, `HEAD`; none leaks 1003 | `a10Probes["GET /calls/{call}"]`, `TestIntegrationA10`; signed in, `TestIntegrationSignedInSameAnswers`; the image, `e2e-a10.sh` |
| The public call's preview names the talkgroup and time on the deployment's clock and holds no recording URL; unavailable is the app's page, 404 | `TestIntegrationCallLinkPreview`; `web.TestPageWithPreview` (escaping, headers); `call-link.spec.ts` against the binary |
| A16, server: in progress, ended with its recording, deleted by retention answers as a hidden call | `TestIntegrationA16CallLink` |
| A16, browser: the card in progress, Listen live adding 1001 for the session, the card playing once the call ends, "This call isn't available" after retention as for a hidden call, over HTTP and in the page | `call-link-live.spec.ts` (`@live`), with the fixture's `call-start`, `call-end` and `call-delete` |
| The card's fields, times in TZ, the list below from the call, oldest first | `call-link.spec.ts` |
| Share from a row, from a replay and from the live call in the dock; the link opens the call's card; copying by hand | `call-link.spec.ts`, `call-link-live.spec.ts` |
| axe in both looks and both themes at 375 and 1,280 px: an ended, an in-progress and an unavailable call | `call-link.spec.ts` |

## Consequences

- A new page whose HTML depends on what its path names (a shared conversation's preview, #82, if it gets one) is a `public.Page`, mounted as a page route, with probes in the A10 harness.
- `web.App.Page` is the only way a page with data in it is written; the shell's own page is unchanged.
- The fixture's `call-start`, `call-end` and `call-delete` commands go with the others when the binary serves the admin API (M4).
