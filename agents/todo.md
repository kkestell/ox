# TODO

- [ ] [Add OpenAI subscription support](plans/2026-10-01-001-openai-subscription.md)

- [x] [Replace apply_patch with write_file and edit_file](plans/2026-09-29-007-simpler-file-tools.md)

- [ ] [OX-0025](issues.csv:26): Unknown slash commands are sent to the model as
      prompts instead of being rejected
- [ ] [OX-0026](issues.csv:27): Temporary OpenAI errors such as 503 end the turn
      instead of being retried
- [ ] [OX-0027](issues.csv:28): The server writes no logs, so provider errors
      are lost after they are shown
- [x] [OX-0028](issues.csv:29): Callers pair a model with a client by hand, and
      three runtime guards check that they match
- [ ] [OX-0029](issues.csv:30): Compaction stops repeating the skill invocation
      once a later turn begins
- [ ] [OX-0030](issues.csv:31): Compaction fires at about half the usable
      context because the byte estimate overshoots
- [ ] [OX-0031](issues.csv:32): A compaction immediately before a subagent's
      final answer loses the report
- [ ] [OX-0032](issues.csv:33): A failed main turn cancels busy subagents and
      drops their results
- [ ] [OX-0033](issues.csv:34): OpenAI requests set no prompt cache key and
      cache hits are erratic
- [ ] [OX-0034](issues.csv:35): Every edit stores the whole file twice in the
      transcript
- [ ] [OX-0035](issues.csv:36): Shell output truncation keeps only the tail
- [ ] [OX-0036](issues.csv:37): The subagent limit counts idle subagents whose
      answers were delivered
- [ ] [OX-0037](issues.csv:38): Reasoning summary parts are joined without a
      separator
- [ ] [OX-0038](issues.csv:39): `wait` with no subagents returns a redundant
      message
- [ ] [OX-0039](issues.csv:40): Transcript entries carry no timestamps
- [ ] [OX-0040](issues.csv:41): Parallel subagents in one workspace contend for
      the cargo build lock
- [ ] [OX-0041](issues.csv:42): The installed `ox-work` skill points at
      `docs/agents/` while this repo uses `agents/`
- [ ] [OX-0042](issues.csv:43): A failed subagent's failure message omits what
      it changed

- [x] [OX-0002](issues.csv:3): Tall approval blocks hide choices and the
      composer
- [x] [OX-0003](issues.csv:4): A second prompt silently replaces a queued prompt
- [x] [OX-0004](issues.csv:5): Joined emoji split across display rows
- [x] [OX-0005](issues.csv:6): Every draw formats the entire transcript
- [x] [OX-0007](issues.csv:8): "Agent message" names two things
