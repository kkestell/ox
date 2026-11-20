### Inaccurate

1. **7 — startup validation is narrower than the promise.** The draft says oxd refuses any configuration it cannot honor. Startup checks catalog structure, defaults, harness names, and Claude effort levels, but does not check whether Claude accepts configured model names or permission modes; those can fail on sending. Evidence: `internal/config/config.go:76-103`, `cmd/oxd/main.go:75-93`, `internal/claude/claude.go:61`, and the deliberately misspelled mode in `internal/server/live_test.go:27-30,125-126`. Corrected sentence: “Ox refuses malformed configuration, invalid defaults, unknown harnesses, and unsupported Claude effort levels at startup, and explains why; other choices Claude rejects surface when the session uses them.”

2. **20 — expanded tool results are text-only, not necessarily the full result.** The draft promises the full input and result. Tool-result extraction discards non-text blocks; the page displays the resulting text. Evidence: `internal/claude/messages.go:84-113`, `cmd/ox/web/src/SessionView.tsx:149-151`. Corrected sentence: “Each tool call shows its name, an input summary, and whether it is running, done, failed, or stopped; expanding it shows the full input and the result’s text, and a plan renders as Markdown.”

3. **25 — failed history writes can make transcripts diverge permanently.** The draft promises the same transcript everywhere and after restart, but the Not met today list covers only failed conversation-token storage, not failed history storage. History insertion failures are logged and discarded while the update is still broadcast, so an existing page can show content absent after reload. Evidence: `internal/server/history.go:42-51`, `internal/server/session.go:144-150`. **Move to Not met today**, with cause: “If recording history fails, Ox still shows the update on connected pages without reporting the recording failure there; reloading or restarting can lose that part of the transcript.”

4. **33 — removing a harness from the configuration hides its stored settings.** The draft says a setting no longer offered still shows. Missing models and modes have fallback displays, but the entire settings form is hidden when the session’s harness is absent from the catalog. Evidence: `cmd/ox/web/src/SessionView.tsx:165-185`; contrast the individual-value fallbacks in `cmd/ox/web/src/SettingsFields.tsx:76,188-191`. **Move to Not met today**, with cause: “Removing a session’s entire harness from the configuration hides its settings; removing only an individual model or permission-mode choice leaves the stored value visible.”

5. **39 — a turn result does not withdraw approvals.** The draft says finishing the turn withdraws approvals. A result deliberately leaves them pending because a background subagent can still need an answer; cancellation occurs on Claude reporting idle, cancelling the request, or exiting. Evidence: `internal/claude/agent.go:149-169,210`, `internal/claude/prompt.go:53-58`, `internal/claude/approval_test.go:68-87`. Corrected sentence: “An approval waits for an answer and prevents parking; it is withdrawn on every page when Claude cancels it, becomes idle, or exits.”

6. **43 — reconnect behavior depends on the event stream, not which executable restarted.** The draft says restarting ox reconnects automatically whereas restarting oxd requires reload. The page makes no such distinction: it retries while the browser keeps the stream reconnecting and asks for reload if the browser closes it; a new stream can connect to oxd whenever its socket is available. Evidence: `cmd/ox/web/src/connection.ts:22,32-45`, `cmd/ox/main.go:81-87`. Corrected sentence: “When the connection drops, the page reports it, fails pending requests, and holds Send and settings until each session reloads. It reconnects automatically while the browser retries the connection, and asks for a reload if the browser closes it.” The existing **Not met today 43** entry is not a violation of its stated requirement: Stop and approval Submit are not Send or settings, which are the controls 43 promises to disable (`cmd/ox/web/src/ox.tsx:179-183`, `cmd/ox/web/src/SessionView.tsx:92-102,171-182`). Drop that entry rather than treating those still-enabled actions as a confirmed breach of 43.

7. **46 — a startup error cannot always show Claude’s own reason.** The draft unconditionally promises Claude’s reason. Ox supplies its own timeout error if initialization hangs, and supplies a process-launch error when Claude cannot be executed. Evidence: `internal/claude/claude.go:98-103,108-127`. Corrected sentence: “A failed Claude startup shows its diagnostic when one is available, or the launch or timeout error otherwise; an unresponsive startup times out after a minute.”

### Missing

1. **“Workspaces and their session membership survive restarting Ox.”** Evidence: `internal/store/store.go:91-132`, `internal/server/server.go:45-63`. Losing the registered workspace list on an ordinary restart would lose the user’s navigation and require rebuilding it; 25 covers transcript persistence and 51 covers sessions, but neither explicitly covers workspace registration.

2. **“Removing a workspace asks for confirmation first.”** Evidence: `cmd/ox/web/src/Sidebar.tsx:46-55,96-109,115-123`. Accidentally removing a workspace and its draft without the existing confirmation is a destructive-action regression users would call a bug.

3. **“A session created after the user switches views does not take them away from their current view.”** Evidence: `cmd/ox/web/src/App.tsx:54-57`, `cmd/ox/web/e2e/session.spec.ts:100-115`. An asynchronous creation unexpectedly replacing the conversation the user moved to interrupts their work.

4. **“Prompts appear in the transcript as plain text, not rendered Markdown or HTML.”** Evidence: `cmd/ox/web/src/SessionView.tsx:116`, `cmd/ox/web/e2e/session.spec.ts:44-79`. Rendering prompt markup would change what the user sees they sent and could make literal instructions misleading.

5. **“Changing a session setting keeps its conversation and leaves settings the user did not change alone.”** Evidence: `internal/server/methods.go:395-405`, `internal/claude/configure.go:34-52`, `internal/server/methods_test.go:174-187`. Losing context or changing an unrelated billing or permission choice during an ordinary setting edit is a functional bug.

6. **“Ox reads its configuration and keeps its state in the user’s XDG directories, using the home-directory defaults when those locations are unset; the user can choose other configuration, database, and socket paths.”** Evidence: `internal/paths/paths.go:10-40`, `cmd/oxd/main.go:27-34`, `cmd/ox/main.go:25-33`. Ignoring the user’s selected locations can load the wrong catalog or session database and leave files somewhere unexpected.

7. **“Starting another daemon against an already running daemon’s socket fails without replacing it, and an unrelated file at that path is left untouched.”** Evidence: `cmd/oxd/main.go:124-138`, `cmd/oxd/main_test.go:41-54`. Replacing a live socket or unrelated file would disrupt existing work or destroy the user’s data.

8. **“A socket left behind by a daemon that has exited does not prevent the next daemon from starting.”** Evidence: `cmd/oxd/main.go:128-142`, `cmd/oxd/main_test.go:27-39`. Requiring manual socket deletion after an abnormal exit is a concrete startup failure the existing implementation avoids.

9. **“A second interruption during daemon shutdown ends the daemon immediately instead of continuing to wait for Claude.”** Evidence: `cmd/oxd/main.go:54-64`, `cmd/oxd/main_test.go:90-92,159-169`. An operator unable to stop a shutdown already waiting on Claude loses an implemented escape hatch.

### Fails the criteria

1. **3 — separate requirements bundled together.** Ordering, session naming, and activity indication are independently observable promises (`cmd/ox/web/src/Sidebar.tsx:27-30,142-148,168-170`). Split into: “A workspace lists its sessions oldest first.” “A session is labeled with the first nonblank line of its first prompt, or a short identifier before it has a prompt.” “Each session shows whether it is parked, idle, running, or waiting on the user.”

2. **5 — defaults and available choices are separate requirements.** Defaults and user selection have distinct behavior (`cmd/ox/web/src/NewSessionView.tsx:12-13,64-80`). Split into: “A new-session draft starts with the configured default harness, model, effort, and permission mode.” “Before sending, the user can choose any offered harness, model, effort, and permission mode.”

3. **22 — turn failure and process exit are separate requirements.** They are rendered independently (`cmd/ox/web/src/SessionView.tsx:121-125`). Split into: “A failed turn shows its error in the transcript.” “A Claude process that exits without Ox stopping it leaves an exit notice in the transcript.”

4. **28 — exceeds the one-or-two-sentence limit and combines inactivity parking with behavior while parked.** Evidence: `internal/claude/park.go:8-9`, `internal/server/session.go:91-99`, `internal/server/methods.go:324-329`. Split into: “An idle session parks after five minutes, with a configurable period and an option to disable parking. It does not park while a prompt, setting change, approval, background task, or schedule is pending.” “A parked session runs no schedules until the next prompt starts it.” The existing Not met today 24 caveat still applies to the first promise’s park-boundary race.

5. **30 — names a cosmetic implementation of pending feedback.** A spinner is a specific icon choice, which the criteria exclude; the user-observable promise is that pending settings are distinguished (`cmd/ox/web/src/SettingsFields.tsx:69-78,202-219`). Rewrite: “A settings change reaches Claude before the next prompt; while it is pending, the page marks the changed setting as pending and holds Send. If it fails, the page shows the error and the settings Claude actually has.”

6. **32 — harness restriction and effort retention are separate requirements.** Evidence: `cmd/ox/web/src/SessionView.tsx:169-174`, `cmd/ox/web/src/SettingsFields.tsx:38-43`. Split into: “An existing session can choose models only from its harness.” “Choosing a model keeps the current effort when the new model offers it.”

7. **47 — working directory and billing are separate requirements.** Evidence: `internal/claude/claude.go:66-73`. Split into: “Claude works in the session’s workspace directory.” “Ox does not use an API key or authentication token inherited from its environment to bill Claude’s work instead of the user’s subscription.”

8. **48 — implementation rather than a defined user-observable promise.** “Ox’s appended style instructions” names an injection mechanism without defining an observable outcome, and the injected content is stylistic (`internal/claude/claude.go:27-29,54`). Drop it from functional requirements; it promises neither reliable model compliance nor a non-cosmetic behavior.

9. **52 — bookkeeping and Claude’s permission boundary are separate requirements.** Evidence: `internal/store/store.go:68-82`, `cmd/oxd/main.go:109-142`, `internal/claude/claude.go:61`, `internal/claude/approval.go:34-47`. Split into: “Ox’s bookkeeping writes only its own database and socket.” “Claude changes the workspace only as its permission mode and the user’s approvals allow.”

### Checked and accurate

1, 2, 3, 4, 5, 6, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 21, 22, 23, 24, 26, 27, 28, 29, 30, 31, 32, 34, 35, 36, 37, 38, 40, 41, 42, 44, 45, 47, 48, 49, 50, 51, 52
