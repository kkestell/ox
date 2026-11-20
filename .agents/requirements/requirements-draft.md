# Ox functional requirements (draft)

What the person using Ox can see and rely on. Ungrouped.

1. The user adds a workspace by giving the absolute path of an existing directory; adding the same directory again changes nothing.
2. A workspace can be removed only when it has no sessions. Removing it asks first, and leaves the directory on disk.
3. Each workspace lists its sessions oldest first.
4. A session is labeled with the first line of its first prompt, or a short identifier before it has one.
5. Each session shows whether it is parked, idle, running, or waiting on the user.
6. The URL names the selected session, so reloading the page reopens it.
7. A new session starts with the configured default harness, model, effort, and permission mode.
8. Before sending, the user can pick any offered harness, model, effort, and permission mode for a new session.
9. Without a configuration file, Ox offers Claude Opus, Sonnet, and Haiku 5.5 with their effort levels and the manual, auto, acceptEdits, and plan modes.
10. `oxd` refuses to start with a malformed configuration, defaults it does not offer, an unknown harness, or an effort level Claude does not take, and says why.
11. Ox reads its configuration and keeps its database and socket in the user's XDG directories, and the user can choose other paths.
12. Submitting a new session creates it and sends the prompt exactly once.
13. A session whose creation finishes after the user has moved to another view does not take them away from it.
14. If creating a session fails, the draft keeps its text and settings and shows the error, with a way back to the draft from any other view, and the user can submit again.
15. If the page loses the reply to a creation but the session exists, the page opens the session with the prompt unsent and asks the user to review its settings and send.
16. A prompt is sent only when the user submits it: showing a view, reconnecting, or reloading sends nothing, and a failed send is never retried automatically.
17. A send cut off by a lost connection shows an error even if Claude received the prompt; the transcript shows whether it did.
18. Unsent text, in a session or in a workspace's new-session draft, survives switching views and reconnecting. Reloading the page discards it, and removing a workspace discards its draft.
19. After a successful send the composer clears, keeping anything typed since the send.
20. A blank prompt is not sent.
21. Enter sends and Shift+Enter starts a new line; Enter while composing with an input method does not send.
22. A session takes one prompt at a time: Send is available only when the session can take one, and prompts are not queued.
23. While Claude works, Send becomes Stop. Stop ends the turn, the transcript shows it was interrupted, and the session takes prompts again.
24. Prompts show in the transcript as the plain text the user sent.
25. Claude's replies stream in as Markdown. Raw HTML shows as text, images are not loaded, unsafe links are not links, and links open in a new tab.
26. Each tool call shows its name, a summary of its input, and whether it is running, done, failed, or stopped; expanding it shows the full input and the result's text, and a plan renders as Markdown.
27. The transcript shows the main conversation only, not the inner messages of Claude's subagents.
28. A failed turn shows its error in the transcript.
29. A Claude process that exits without Ox stopping it leaves the reason in the transcript.
30. The transcript follows new output unless the user has scrolled up.
31. Turns Claude starts itself, from a schedule or a finished background task, run and appear in the transcript like any other turn.
32. The transcript is the same on every page and after a reload.
33. Workspaces, sessions, their settings, and their transcripts survive parking and `oxd` restarts.
34. A parked session resumes its Claude conversation on the next send.
35. If Claude can no longer resume the conversation, the send fails with a notice in the transcript, and the next send starts a new conversation without the earlier context.
36. An idle session parks after five minutes; the period is configurable, and parking can be turned off. It does not park while a prompt, setting change, approval, background task, or schedule is pending.
37. A parked session runs no schedules until the next prompt starts it.
38. The settings shown for a session are the ones Claude runs with.
39. A settings change reaches Claude before the next prompt. While it is pending, the page marks the changed setting as pending and holds Send. If it fails, the page shows the error and the settings Claude actually has.
40. Changing a setting keeps the conversation and leaves the other settings alone.
41. Settings can change only while the session is idle or parked.
42. An existing session can change its model only within its harness.
43. Choosing a model keeps the effort when the new model offers it.
44. A model or permission mode the configuration no longer offers still shows as the session's setting.
45. When Claude changes its own permission mode, as when leaving plan mode, the page shows the new mode and later processes use it.
46. Changes made from another page, to workspaces, sessions, settings, or a conversation, appear on every page as they happen.
47. A tool permission request appears as a card above the composer on every page, including after a reload, until it is answered or withdrawn.
48. Allow sends the tool's input exactly as the card shows it; Deny, or a typed reply, denies with that message.
49. A question from Claude appears as a form with its options and an Other answer; the user submits answers to every question or declines.
50. An approval waits for an answer, and the session does not park meanwhile. It is withdrawn on every page when Claude cancels it, goes idle, or exits.
51. An approval already answered, from this page or another, refuses a second answer with an error.
52. Every request ends in its result or an error, shown in the view that made it. The error stays until that view's next request, even if a later request finishes first.
53. One session starting, changing settings, or running never holds up another session.
54. When the connection drops, the page says so, fails its pending requests, and holds Send and settings until each session has reloaded. It reconnects by itself, unless `oxd` is still down when it tries, in which case it asks for a reload.
55. Closing every page leaves prompts, approvals, and Claude's own turns running.
56. Deleting a session asks first, then stops Claude, deletes Ox's history of it, leaves Claude's transcript and the workspace on disk, and removes it from every page.
57. A failed Claude startup fails the send within a minute and shows why, in Claude's own words when it gives a reason.
58. Claude works in the session's workspace directory.
59. Claude's work is billed to the user's Claude subscription, never to an API key or token in Ox's environment.
60. Claude runs with Ox's appended style instructions.
61. Only pages served from this machine (or from the host `ox` was told to serve) can reach `oxd`.
62. A prompt over 1 MiB is refused with an error and stays in the composer.
63. Stopping `oxd` stops every Claude process it runs; a second stop request ends it at once.
64. A socket left by an `oxd` that has exited does not block the next start; `oxd` will not replace a socket another `oxd` is serving, or a file that is not a socket.
65. Ox's own bookkeeping writes only its database and socket.
66. Claude changes the workspace only as its permission mode and the user's approvals allow.

## Not met today

Confirmed from the source. Each needs a fix, an entry in `docs/known-issues.md`, or a change to the requirement.

- **31:** a turn Claude starts at the moment the park timer fires can be killed, because the session parks without checking again.
- **32, 33:** a failed history write is only logged. Open pages show the update, but it is missing after a reload.
- **34:** if saving the conversation token fails, the next start silently begins a new conversation, with none of 35's notice.
