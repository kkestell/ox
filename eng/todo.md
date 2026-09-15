# TODO

- [ ] Keep the todo list out of the cached request prefix. The serialized todo
      entries sit between the system prompt and all history, so every todo write
      invalidates the provider's prompt cache for the whole conversation. Move
      the message to the tail of the request and keep compaction's newest-group
      walk from treating it as the newest message group.
