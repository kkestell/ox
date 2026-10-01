# TODO

- [ ] [Add OpenAI subscription support](plans/2026-10-01-001-openai-subscription.md)

- [x] [Replace apply_patch with write_file and edit_file](plans/2026-09-29-007-simpler-file-tools.md)

- [ ] [OX-0025](issues.csv:26): Unknown slash commands are sent to the model as
      prompts instead of being rejected
- [ ] [OX-0026](issues.csv:27): Temporary OpenAI errors such as 503 end the turn
      instead of being retried
- [ ] [OX-0027](issues.csv:28): The server writes no logs, so provider errors
      are lost after they are shown
- [ ] [OX-0028](issues.csv:29): Callers pair a model with a client by hand, and
      three runtime guards check that they match

- [x] [OX-0002](issues.csv:3): Tall approval blocks hide choices and the
      composer
- [x] [OX-0003](issues.csv:4): A second prompt silently replaces a queued prompt
- [x] [OX-0004](issues.csv:5): Joined emoji split across display rows
- [x] [OX-0005](issues.csv:6): Every draw formats the entire transcript
- [x] [OX-0007](issues.csv:8): "Agent message" names two things
