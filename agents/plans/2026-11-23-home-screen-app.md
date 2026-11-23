# Open Ox from the iPhone Home Screen as a standalone app

## Goal

The user reaches Ox from an iPhone over plain HTTP at the tailnet listener,
`http://100.110.111.81:17840`. Added to the Home Screen today, Ox gets no icon
and no manifest. Once this change lands, Safari's Add to Home Screen gives Ox its icon and
name, and the Home Screen icon opens Ox full screen without Safari's toolbars,
over the same plain-HTTP URL. A standalone app has no browser reload control,
so the page's request to reload after a lost connection becomes a Reload
button.

## Related code

- `cmd/ox/web/index.html` — the page's `<head>`. It already sets
  `viewport-fit=cover`.
- `cmd/ox/web/src/style.css` — the palette (`--background`, `--card`,
  `--pane`) and `.layout`, which pads by `env(safe-area-inset-*)` and sizes from
  the custom properties `viewport.ts` writes.
- `cmd/ox/web/src/viewport.ts` — sizes the page from `visualViewport`.
- `cmd/ox/main.go` — `handler` serves the embedded `web/dist` at `GET /` with
  `http.FileServerFS`, with no host check, the same for every listener.
- `cmd/ox/web/src/connection.ts` — `Disconnected`, and the two lost-connection
  messages; the closed-stream one says "Reload the page to reconnect."
- `cmd/ox/web/src/ox.tsx` — the provider's page `error` signal, set by `retry`
  and by the connection's `onError`.
- `cmd/ox/web/src/App.tsx:151` — renders the page error as an alert; line 154
  is the established alert-with-button pattern ("Reopen draft").
- `cmd/ox/web/e2e/session.spec.ts` — the lost-connection tests, using
  `services.restartRelay()`.

## Decisions

- **Plain HTTP is enough.** Standalone mode needs no HTTPS. WebKit's
  [Safari 26.0 release post](https://webkit.org/blog/17333/webkit-features-in-safari-26-0/)
  says that on iOS and iPadOS 26 every site added to the Home Screen opens as a
  web app by default, with "zero requirements for installability", and that
  before 26 a site needed `apple-mobile-web-app-capable` or a manifest
  `display` value. Neither Apple nor WebKit documents a secure-context
  requirement for either path. HTTPS gates service workers and Web Push, and Ox
  uses neither. Ox keeps its plain-HTTP tailnet listener. No `tailscale serve`
  and no certificate.
- **Manifest.** `cmd/ox/web/public/manifest.json`, linked from `index.html`:

  ```json
  {
    "name": "Ox",
    "display": "standalone",
    "start_url": "/",
    "icons": [{ "src": "/icon.png", "sizes": "512x512", "type": "image/png" }]
  }
  ```

  `display: standalone` opens the app full screen on iOS 16.4 through 18, and
  states the intent on 26, where it is already the default. `start_url` opens
  the app at the session list, as a fresh page load does. Scope defaults to
  `/`, which covers every page URL Ox makes. The manifest is the single source
  of the icon: Safari has used manifest icons since 15.4, and the 26.0 post
  says "If you define your icons in the manifest, they're used". There is no
  `apple-touch-icon` link. It is named `.json` so `http.FileServerFS` serves it
  as `application/json`. Go's built-in table has no `.webmanifest` type.
  Ox omits `apple-mobile-web-app-capable` because the manifest covers the same
  iOS versions.
- **Status bar.** No `apple-mobile-web-app-status-bar-style`, so standalone uses
  the default opaque status bar: the page starts below it, and
  `env(safe-area-inset-top)` is 0. `black-translucent` would draw the page under
  the bar, and WebKit bug
  [301994](https://bugs.webkit.org/show_bug.cgi?id=301994) records iOS releases
  (26.1, 26.5.2, 27.x) where that inset reads 0 in standalone and the clock
  overlaps the page's top row. The default bar avoids that bug on every release.
- **Theme colors.** Two `<meta name="theme-color">` tags, with
  `media="(prefers-color-scheme: light)"` `#ffffff` and
  `media="(prefers-color-scheme: dark)"` `#101010`. These are `--pane` in each
  scheme, the background of the session header that sits under the status bar
  on a narrow screen. In dark mode `--pane` mixes oklch 0.145 and 0.205 in
  oklab to L 0.175, which is `#101010` in sRGB. A comment beside the tags names
  `--pane` as their source. The manifest has no `theme_color`, because it
  cannot vary by scheme and the meta tags take precedence.
- **Layout in standalone.** No change to `viewport.ts` or `.layout`. Without
  Safari's toolbars, `visualViewport` reports the area below the status bar.
  The keyboard still covers the page without resizing the layout viewport, so
  `--keyboard-height` stays as it is. `env(safe-area-inset-bottom)` is the home
  indicator's inset at the screen edge, which `--bottom-inset` already takes.
  The side insets in landscape are already padded.
- **Icon.** Ox's own icon, from its earlier port: a white ring on a `#202020`
  tile. Its source is `cmd/ox/web/icon.svg`, copied from that port's
  `icon.svg` with the `rect`'s `rx="112"` removed. The tile is then full bleed,
  because iOS applies its own corner mask, and fills transparent corners with
  black:

  ```svg
  <svg xmlns="http://www.w3.org/2000/svg" width="512" height="512" viewBox="0 0 512 512">
    <rect width="512" height="512" fill="#202020"/>
    <circle cx="256" cy="256" r="140" fill="none" stroke="#ffffff" stroke-width="64"/>
  </svg>
  ```

  The SVG sits outside `public/`, so it does not ship. `cmd/ox/web/public/icon.png`
  is rendered from it once with `rsvg-convert -w 512 -h 512 -o
  public/icon.png icon.svg`, run in `cmd/ox/web`, and committed; the commit
  body gives the command. Safari does not take SVG icons, so the Home Screen
  needs a PNG. One 512×512 PNG covers every size: iOS scales the manifest icon
  to its Home Screen size, 180×180 on current iPhones, and 512 is the size the
  manifest specification's other readers expect. Every pixel is opaque, because
  the tile is full bleed.
- **Serving.** Vite copies `cmd/ox/web/public/` to the root of `dist`, which
  `ox` embeds and serves at `/manifest.json` and `/icon.png` through the
  existing `GET /` handler, on every listener. No Go change. `make dev`, with or
  without the dev-one-port change, serves the same built `dist`.
- **Requirements.**
  - *Lost connection:* in a standalone app, the request to reload has no
    browser control behind it. The closed-stream alert reads "Lost the
    connection to ox." and carries a Reload button that calls
    `location.reload()`, following the "Reopen draft" alert's pattern. Reload
    keeps the URL, so *Session URL* holds.
  - *Touch screens:* the controls stay clear of the status bar, home indicator,
    and rounded corners in standalone, through the existing insets.
  - *Session URL:* a launch from the Home Screen loads `/`. Navigation within
    Ox stays in scope and updates the URL as before.
  - *Replies as Markdown:* links still open with `target="_blank"`. Standalone
    iOS opens them outside the app.
- **Construction.** The page `error` signal in `ox.tsx` holds the `Error`
  instead of its message. `Disconnected` gets a `readonly closed: boolean`, set
  by `connection.ts` from `source.readyState`, which also chooses the message.
  `App.tsx` shows the Reload button exactly when the page error is a
  `Disconnected` with `closed`. That leaves one source, with no flag kept
  beside the message. `retry` stores the caught `Error` and compares identity
  as it compares strings today.
- **Trust boundaries.** The two new files are public static assets on the
  existing unauthenticated `GET /`, like `index.html`. Nothing reaches `oxd`.

## Check your assumptions

1. **The app icon.** Changed: the user chose the icon from Ox's earlier port,
   a white ring on a `#202020` tile, drawn full bleed for iOS's corner mask,
   over Lucide's `square-terminal`.
2. **Home Screen app requirement (added).** Kept, as written.
   - **Plan:** add "**Home Screen app.** On an iPhone, Ox added to the Home
     Screen from Safari shows its icon and the name Ox, and opens full screen
     without Safari's toolbars, at the session list, over the same address the
     browser uses."
   - **Alternative:** open at the session that was showing when Ox was added.
     Every launch would then go to an old session.
3. **Reload button after a lost connection (Lost connection, changed).** Kept.
   - **Plan:** when Ox cannot reconnect, the message reads "Lost the connection
     to ox." with a Reload button, in Safari and in the Home Screen app alike.
     The Home Screen app has no reload control of its own.
   - **Alternative:** keep "Reload the page to reconnect." In the Home Screen
     app, the user would then have to swipe Ox away in the app switcher and
     reopen it.
   - Old: "**Lost connection.** When the connection drops, the page says so,
     fails its pending requests, and holds Send and settings until each session
     has reloaded. It reconnects by itself, unless `oxd` is still down when it
     tries, in which case it asks for a reload."
   - New: "**Lost connection.** When the connection drops, the page says so,
     fails its pending requests, and holds Send and settings until each session
     has reloaded. It reconnects by itself, unless `oxd` is still down when it
     tries, in which case it shows a Reload button."

## Test plan

- The page links a manifest that opens standalone, and the icon it names is
  served. This is a new e2e test in `session.spec.ts`. It reads the
  `link[rel=manifest]` href from the loaded page, fetches it with
  `page.request` from the test's `ox`, and asserts `name` `Ox`, `display`
  `standalone`, and `start_url` `/`. It fetches the manifest's icon `src` and
  asserts status 200 and `image/png`. No existing test reads the page's head
  or static files.
- When the stream closes, the alert carries a Reload button that reconnects.
  This is a new e2e test in `session.spec.ts` beside the lost-connection tests.
  It routes `/events` to answer 502, then calls `services.restartRelay()`.
  The browser closes the stream on the 502. The test expects the alert "Lost
  the connection to ox." and a Reload button, unroutes `/events`, clicks
  Reload, and expects no alert and Send enabled. No existing test reaches the
  closed-stream state.
- Standalone on the device: the user adds
  `http://100.110.111.81:17840` to the Home Screen from Safari on the iPhone
  and checks the following:
  - The icon and name appear.
  - Launching shows no Safari toolbars.
  - The status bar matches the page in light and dark mode.
  - The session header clears the status bar.
  - The composer sits above the home indicator, and above the keyboard while
    typing.
  - Rotating to landscape keeps the controls clear of the corners.

  Headless Chrome cannot emulate iOS standalone mode, so the implementing
  agent reports this check as not run, blocked on the physical device.

## Implementation plan

1. Add `cmd/ox/web/icon.svg` as given in Decisions, and render
   `cmd/ox/web/public/icon.png` from it with `rsvg-convert`. Confirm with
   `magick identify -format '%wx%h %[opaque]' public/icon.png` that it is
   `512x512 True`.
2. Add `cmd/ox/web/public/manifest.json` as given above. Run `npm run fix` so
   Biome formats it.
3. In `cmd/ox/web/index.html`, add `<link rel="manifest" href="/manifest.json">`
   and the two `theme-color` meta tags, with a comment naming `--pane`.
4. In `cmd/ox/web/src/connection.ts`, give `Disconnected` a `readonly closed:
   boolean` constructor parameter. Pass `source.readyState ===
   EventSource.CLOSED` with the message "Lost the connection to ox.", or
   `false` with "Lost the connection to ox. Reconnecting."
5. In `cmd/ox/web/src/ox.tsx`, make the page `error` signal
   `createSignal<Error>()`. `retry` stores the caught `Error` and clears only
   when the error shown is still its own, and `onError` stores the
   `Disconnected`.
6. In `cmd/ox/web/src/App.tsx`, render the page error's `message`, followed by
   a `Reload` button calling `location.reload()` when the error is a
   `Disconnected` with `closed`, in the same form as the "Reopen draft"
   alert. Update any other reader of `ox.error()` to read `.message`.
7. Add the two e2e tests.
8. Apply the requirement changes to `docs/functional-requirements.md`: add
   **Home Screen app** under Pages and connection after **Web access**, and
   change **Lost connection**.

## Requirement changes

- **Added:** **Home Screen app.** On an iPhone, Ox added to the Home Screen from
  Safari shows its icon and the name Ox, and opens full screen without Safari's
  toolbars, at the session list, over the same address the browser uses.
- **Changed:** **Lost connection.**
  - Old: "When the connection drops, the page says so, fails its pending
    requests, and holds Send and settings until each session has reloaded. It
    reconnects by itself, unless `oxd` is still down when it tries, in which
    case it asks for a reload."
  - New: "When the connection drops, the page says so, fails its pending
    requests, and holds Send and settings until each session has reloaded. It
    reconnects by itself, unless `oxd` is still down when it tries, in which
    case it shows a Reload button."
