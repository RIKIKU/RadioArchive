# What the server needs to provide for Android Auto and for listening that runs only while switched on

Research for issue #4. Terms follow `GLOSSARY.md`: call, live call, recording, talkgroup, listening set.

## Question

A future Android phone app must play live calls without the web app open, run only while the listener has listening switched on (no always-on background service), and show the call playing now (talkgroup name and unit) in Android Auto. What does Android require of such an app, and what must the server's API therefore provide?

## Short answer

Android lets the app do this without anything exotic: a Media3 `MediaLibraryService` running as a `mediaPlayback` foreground service while listening is on, and stopped when it is off. Android Auto then draws its own UI from that service's browse tree and from the media session's now-playing metadata. The hard constraint comes from Media3's lifecycle. If the player stays paused, stopped or failed for more than 10 minutes, the service leaves the foreground and the system can destroy it. Gaps between calls on a quiet listening set often last longer than that. So while listening is on, the app has to present the listening set as one live item that keeps playing, with silence between calls, and swap the now-playing metadata as each call starts.

For the server, that means:

1. **Live audio over plain HTTP in a format ExoPlayer can play**, either one continuous live stream per listening set or per-call audio that starts while the call is still in progress.
2. **A push feed of call events** (call started or ended, talkgroup, unit) that the app can follow from the background, resumable after a network drop.
3. **Now-playing metadata per call**: talkgroup display name and unit display name, carried in the event feed or embedded in the stream.
4. **A small, fast talkgroup catalogue** that the app can turn into a browse tree of no more than four browsable tabs, without pagination. That means server-side grouping of the roughly 1,000 talkgroups.
5. **Talkgroup search by name**, for voice commands.
6. **No sign-in for any of it**, with talkgroups that are not public kept out of the catalogue, search, event feed and audio.

## What Android requires of the app

### Media session and library service

- Android Auto finds and browses a media app through a `MediaBrowserService` or Media3 `MediaLibraryService` plus a `MediaSession`. Even apps built with Car App Library templates still need them. ([Build media apps for cars](https://developer.android.com/training/cars/media))
- In the browse tree, `FLAG_BROWSABLE` items are nodes and `FLAG_PLAYABLE` items are leaves. An item that is both "operates like a playlist". (same source)
- `MediaLibrarySession` adds `onGetLibraryRoot()`, `onGetChildren()` and `onGetSearchResult()` to the session callback. ([Serve content with a MediaLibraryService](https://developer.android.com/media/media3/session/serve-content))
- `onGetRoot` must return quickly: "Don't perform checks for user authentication or other slow processes within your service's `onGetRoot` implementation." ([Build content hierarchy](https://developer.android.com/training/cars/media/create-media-browser/content-hierarchy))
- "Android Auto and AAOS don't support pagination", so `onGetChildren` must return each level whole. "Android Auto and AAOS have strict limits on how many media items can be displayed at each level in the menu." (same source)
- Root hints: "In most cases, expect this number to be four, meaning that only four (or fewer) tabs can be shown", and only browsable items can be root tabs. (same source)

### Background playback and the foreground service

- The service must declare `android:foregroundServiceType="mediaPlayback"` and the `FOREGROUND_SERVICE` and `FOREGROUND_SERVICE_MEDIA_PLAYBACK` permissions. ([Background playback with a MediaSessionService](https://developer.android.com/media/media3/session/background-playback))
- `mediaPlayback` is for "Continue audio or video playback from the background". It has no runtime-permission prerequisite and no time limit, unlike `dataSync`, `mediaProcessing` or `shortService`. Apps targeting Android 15+ cannot launch it from `BOOT_COMPLETED`. ([Foreground service types](https://developer.android.com/develop/background-work/services/fgs/service-types))
- Apps targeting Android 12+ cannot start foreground services from the background, apart from listed exemptions such as the user acting on the app's notification, widget or activity. ([Restrictions on starting a foreground service from the background](https://developer.android.com/develop/background-work/services/fgs/restrictions-bg-start)) This suits "listening only while switched on": the switch is a user action.
- Media3 manages the foreground state and a media notification, and "The notification cannot be removed while the foreground service is running." ([Background playback](https://developer.android.com/media/media3/session/background-playback))
- **The key constraint:** "If the player is paused, stopped or failed for more than 10 minutes without further user interactions, the service is automatically transitioned out of the foreground service state so it can be destroyed by the system." (same source) A listening set can easily go 10 minutes without a call. If the app models listening as "play each call, then go idle", it will be killed during quiet spells. It must stay in a playing state while listening is on.
- By default, if the user swipes the app out of recent tasks, the service keeps running while playback is ongoing and stops otherwise. `pauseAllPlayersAndStopSelf()` can stop it every time instead. (same source) Turning listening off should release the player, which ends the service. Nothing should run while listening is off.

### Now-playing metadata

- The notification and other controllers show the current `MediaItem`'s `MediaMetadata` (title, artist, artwork). To update an item's metadata "use `Player.replaceMediaItem` to update the metadata without interrupting playback." ([Background playback](https://developer.android.com/media/media3/session/background-playback))
- Metadata can also arrive inside the stream. `Player#getMediaMetadata` "is populated from both" static format metadata and dynamic metadata delivered during playback (`onMetadata`). ([Retrieving metadata](https://developer.android.com/media/media3/exoplayer/retrieving-metadata)) HLS ID3 and DASH EMSG timed metadata are supported. ([Supported formats](https://developer.android.com/media/media3/exoplayer/supported-formats))
- The natural mapping is title = talkgroup name and artist/subtitle = unit.

### Artwork

- Artwork given to Android Auto must be a local `content://` or `android.resource://` URI. Remote images are downloaded by the app and exposed through its own content provider. ([Display media artwork](https://developer.android.com/training/cars/media/create-media-browser/media-artwork)) The server does not need to serve images. Static icons in the app are enough.

### Live audio playback

- ExoPlayer plays progressive streams (MP3, Ogg with Opus or Vorbis, ADTS AAC, M4A/FMP4, WebM and others) and adaptive live streams (HLS, including Apple low-latency HLS, and DASH, including low-latency CMAF). RTSP support is limited. WebSocket or WebRTC audio transports are not listed. ([Supported formats](https://developer.android.com/media/media3/exoplayer/supported-formats))
- "Unlike adaptive livestreams, progressive livestreams don't have a live window and can only be played at one position." Adaptive live streams support target live offset and catch-up speed control. If the player falls behind the window, it reports `ERROR_CODE_BEHIND_LIVE_WINDOW` and should jump back to the live edge. ([Live streaming](https://developer.android.com/media/media3/exoplayer/live-streaming))
- Audio codecs depend on platform decoders. Opus, AAC and MP3 are standard Android formats, and Media3 also ships optional software decoders. (same source)

### Android Auto quality requirements that shape the server

From [Car app quality](https://developer.android.com/docs/quality-guidelines/car-app-quality?category=media):

- `MA-1`: "The app must not autoplay on startup or without user initiated action". This fits listening that runs only while switched on.
- `VC-1`: "The app must support Gemini and Google Assistant voice commands." Voice actions arrive as `onPlayFromSearch`. The app "must handle an empty query string" and should match categories such as "radio station" or "playlist". ([Support voice actions](https://developer.android.com/training/cars/media/voice-actions))
- `DR-2`/`DR-3`: launch in no more than 10 seconds, and "load content in no more than 10 seconds". `DR-1`: buttons respond within two seconds.
- `EP-2`: when relaunched, "restore the app state as closely as possible to the previous state."
- `VI-1`: if the user must use the phone, tell them to do so only when safe. Errors use `STATE_ERROR` with a user-facing localized message. ([Handle errors](https://developer.android.com/training/cars/media/errors)) With no sign-in, Android Auto has no sign-in flow to handle at all.

## What the server's API must therefore provide

1. **Live audio transport over HTTP(S) that ExoPlayer can play natively.** One of two shapes:
   - **A continuous live stream for a listening set** (for example LL-HLS, or progressive Ogg/Opus or MP3 with silence between calls). The app plays one never-ending item while listening is on, which satisfies the 10-minute rule by construction. The server needs to accept the listening set as a request parameter, for example a list of talkgroup IDs. This costs one encoder per distinct listening set, which is fine at about 6 listeners at peak.
   - **Per-call live audio plus an event feed.** The app keeps its own always-playing player (silence between calls) and plays each live call's audio as it arrives. The server must then serve a call's audio *while the call is in progress*, for example chunked HTTP of the growing file or a short LL-HLS playlist per call. Serving it only as a finished recording would not give live calls.

   Either way it is plain HTTP. No WebRTC or WebSocket audio, which ExoPlayer does not play without custom code.
2. **A push feed of call events** (SSE or WebSocket, which the app reads in its own code rather than through ExoPlayer) filtered by listening set: call started, call ended, talkgroup ID, unit ID, timestamps. It must be resumable (`since` cursor or last-event-ID) so the app recovers after a tunnel or network drop without replaying stale calls.
3. **Display metadata for now-playing:** talkgroup display name (and short name or group), and unit display name, falling back to unit ID. Deliver it in the event, embed it in the continuous stream as timed metadata (HLS ID3 / DASH EMSG), or both. If more than one unit speaks in one call, the event feed should report unit changes so the app can update the subtitle.
4. **A talkgroup catalogue endpoint** returning all public talkgroups with ID, name and grouping (category or tag) in one fast, cacheable response. The app builds a browse tree from it with at most four browsable root tabs (for example "Listening set", group categories, "Recent") and lists without pagination. With about 1,000 talkgroups, server-side grouping is required so no single level becomes an unusable flat list.
5. **Talkgroup search by name** (case-insensitive, partial match) so the app can answer "play <talkgroup name>" from `onPlayFromSearch` without loading the whole catalogue in the car. An empty query maps to "play my listening set".
6. **Listening set without sign-in.** Either the app keeps the listening set on the device, which is enough for `EP-2`, or the server offers an anonymous per-device listener ID to save it under. Nothing in the flow may need a login. `onGetRoot` must answer immediately.
7. **Enforcement of talkgroups that are not public** in every one of the endpoints above. The app cannot be trusted to filter them.
8. **Machine-readable errors** (talkgroup unavailable or hidden, server unreachable, stream ended) so the app can map them to `STATE_ERROR` messages in Android Auto.
9. **Optional:** artwork is not needed from the server (Android Auto takes only local URIs), and nothing needs to run on the server for a phone whose listening is off. There is no push notification or always-on connection.

## Open points for the spec

- Pick between a continuous per-listening-set stream and per-call live audio with an app-side silent player. The first is simpler for the phone app. The second reuses the web listener's transport. Decide this together with the web listener's live-call transport. The stack decision (#7, which this ticket blocks) should make sure the chosen stack can produce it.
- How talkgroups are grouped for browsing (categories or tags) needs to come from the talkgroup setup that is migrated in.

## Sources

- https://developer.android.com/training/cars/media
- https://developer.android.com/training/cars/media/create-media-browser/content-hierarchy
- https://developer.android.com/training/cars/media/create-media-browser/media-artwork
- https://developer.android.com/training/cars/media/voice-actions
- https://developer.android.com/training/cars/media/errors
- https://developer.android.com/docs/quality-guidelines/car-app-quality?category=media
- https://developer.android.com/media/media3/session/background-playback
- https://developer.android.com/media/media3/session/serve-content
- https://developer.android.com/media/media3/exoplayer/live-streaming
- https://developer.android.com/media/media3/exoplayer/supported-formats
- https://developer.android.com/media/media3/exoplayer/retrieving-metadata
- https://developer.android.com/develop/background-work/services/fgs/service-types
- https://developer.android.com/develop/background-work/services/fgs/restrictions-bg-start
