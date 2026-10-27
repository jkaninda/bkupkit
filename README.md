# bkupkit

The shared core of `pg-bkup`, `mysql-bkup`, `mongodb-bkup`, `libsql-bkup` and `volume-bkup`.

A tool only implements an `engine.Engine`: what to back up, how to write a raw dump to an `io.Writer` and how to replay it from an `io.Reader`. bkupkit handles everything else, the same way in every tool:

| Concern | Package |
|---|---|
| Settings from flags, env (with deprecated fallbacks and `_FILE` secrets), YAML and defaults | `config` |
| Streaming pipeline: dump → compress → encrypt → upload, and back | `pipeline` |
| gzip (parallel) / zstd, detected by magic bytes on restore | `compress` |
| GPG and age, fail-closed ([encryptor](https://github.com/jkaninda/encryptor)) | `crypt` |
| Local, S3, GCS, Azure, SFTP, FTP ([go-storage](https://github.com/jkaninda/go-storage)) | `store` |
| Artifact names `<name>_<YYYYMMDD_HHMMSS><ext>` | `naming` |
| Backup index, sidecars, retention, reconcile | `index` |
| External processes without a shell, safe kill semantics | `execx` |
| Email (SMTP or [Posta](https://github.com/goposta/posta-go)), Telegram, Slack, Discord, ntfy, webhook, healthcheck pings | `notify` |
| Cron daemon, health state, Prometheus metrics | `daemon` |
| The command line | `cli` |

## Writing a tool

```go
func main() {
	(&cli.App{Engine: &myEngine{}, Version: Version, Short: "Back up X"}).Main()
}
```

The engine contract (`engine/engine.go`):

| Method | Notes |
|---|---|
| `Tool()`, `Kind()` | `"pg-bkup"`, `"postgres"`. Used for `{tool}` and `{engine}` in the index name. |
| `Settings(r)` | Register engine settings. Call `config.RegisterDatabase(r, port)` for DB tools. `r.Override(key, fn)` can rebind a common setting's flag. |
| `Configure(ctx, v)` | Validate the resolved config. |
| `Targets(ctx, v)` | One target per database or volume for this run. Set `Target.Values = v`. |
| `Ext(t)` | `".sql"`, `".tar"`, `".dump"`. With native compression, include it (`".archive.gz"`). |
| `NativeCompression(t)` | true if `Dump` output is already compressed. The pipeline then passes it through untouched. |
| `Ping`, `ServerVersion` | `Ping` failures exit with code 3. |
| `Dump(ctx, t, w)` | Write the raw dump to `w`. Return only after the dump fully succeeded. Use `execx.Run` with `Stdout: w`. |
| `Restore(ctx, t, r, info)` | Replay from `r`. Use `execx.Run` with `Stdin: r`. If `r` fails, `execx` kills the tool before it sees EOF, so nothing is committed. |
| optional `engine.Migrator` | Adds the `migrate` command. |
| optional `engine.NameSuffixer` | Adds an infix to names (`_schema_`, `_tables_N_`). |

Rules for engines:

- Never use `sh -c`. Pass arguments as a slice.
- Never put secrets in argv or `os.Setenv`. Use the per-command `execx.Cmd.Env`, or a 0600 file in a `execx.PrivateDir(v.String(config.TmpDir))` that you remove with `defer`.
- Never call `os.Exit` or `log.Fatal`. Return errors.
- Never buffer a whole dump in memory or on disk.

## Commands

```
backup     back up now, or as a daemon with --cron-expression
restore    stream a backup back (-f FILE)
verify     download, decrypt, decompress and check the sha256 without restoring
list       history from the index (--storage-scan lists the storage instead)
delete     remove a backup, its sidecar and its index entry
prune      apply retention (--dry-run)
reconcile  rebuild or repair the index (--from-index OLD to rename)
config     show the resolved config (secrets redacted); --env-docs prints the env reference
health     Docker HEALTHCHECK for daemon mode
migrate    stream a database into TARGET_DB_* (engines that support it)
version
```

Exit codes: `0` ok, `1` failure, `2` configuration, `3` connection, `4` storage, `5` encryption.

## Output contract

In text mode, the last line of a successful backup target is:

```
level=INFO msg="Backup completed" database=shop file=shop_20261027_031500.sql.gz.gpg size=… bytes=<stored size> sha256=<hex> duration=… storage=s3 location=…
```

Nothing that looks like an artifact name is printed after it: index and sidecar names are never logged, and retention runs before it. Consumers can take the last file name in the output. For a machine-readable result, use `--result-file PATH` or `LOG_FORMAT=json`.

## Backup index

Each storage directory holds an index file, `BACKUP_INDEX_NAME` (default `{tool}-metadata.json`; placeholders `{tool}` `{engine}` `{name}` `{db}`), plus one `<artifact>.meta.json` sidecar per backup. Retention only deletes entries recorded in the index, and always keeps the newest backup.

- Index updates use conditional writes (S3 `If-Match`, GCS generations, Azure ETags, local lock files). On SFTP and FTP they fall back to a lock file with an expiry.
- `reconcile` rebuilds the index from sidecars whenever it drifts.
- Index names are validated: plain file names ending in `.json`, never `info.json`, and never anything that looks like a backup file.

## Memory

A backup holds fixed-size buffers only:
- the S3 part size × concurrency (`S3_PART_SIZE` 64 MiB × `S3_UPLOAD_CONCURRENCY` 2 by default);
- about 20 MiB of compression buffers.

The test suite streams 256 MiB through zstd and age with under 64 MiB of live heap and no temp files.

The age and GPG **passphrase** modes run a key-derivation function once per backup or restore. age's scrypt needs about 256 MiB of memory for that moment. Use recipient keys (`AGE_RECIPIENTS`, `GPG_PUBLIC_KEY`) on small containers.

Maximum S3 object size is 10,000 × `S3_PART_SIZE`, so 640 GiB at the default. Raise `S3_PART_SIZE` for larger dumps.

## License

MIT License, Copyright (c) 2024 Jonas Kaninda. See [LICENSE](LICENSE).
