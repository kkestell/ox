# Documentation

Apply this lens to documentation associated with the code under review.

- Document caller obligations that are not clear from names, interfaces, and
  code. Do not require a ceremonial comment on every public item.
- Check changed documentation against actual behavior. Keep durable design
  decisions in their owning documents; local changes need no architecture
  rewrite.
- Guides describe shipped usage. Avoid duplicating requirements, test setup,
  and implementation mechanics across documents.
