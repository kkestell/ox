# field-sync

Uploads survey records from field tablets to the records API, and retries them
until the server accepts them.

```sh
go run ./cmd/field-sync -api https://records.example.com
```
