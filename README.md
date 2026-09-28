# Tracker MCP

An MCP server that lets people with no database knowledge keep simple tables ("trackers")
through an AI assistant. Examples: an interview tracker for HR, or a per-person task tracker for a team.

People can create a tracker, define its columns, add, edit and delete rows, and query rows with filters.

## Quick start

```bash
brew services start postgresql@17        # local Postgres (this repo assumes port 5433)
make db-create                           # creates tracker_mcp and tracker_mcp_test
make build
make test                                # unit + end-to-end tests (resets tracker_mcp_test)
make lint
```

Migrations run automatically on startup.

### Connect a client

**stdio** (Claude Desktop / Claude Code, one process per user):

```json
{
  "mcpServers": {
    "tracker": {
      "command": "/absolute/path/to/tracker-mcp/bin/tracker-mcp",
      "args": ["--transport", "stdio"],
      "env": {
        "DATABASE_URL": "postgres://localhost:5433/tracker_mcp?sslmode=disable",
        "TRACKER_ACTOR": "you@company.com"
      }
    }
  }
}
```

**Streamable HTTP** (one shared server for a team): run `make run-http`, then point the client at `http://127.0.0.1:8080/mcp`. The server also serves `/healthz`.

### Configuration

| Flag | Env | Default |
|---|---|---|
| `--transport` | `TRACKER_TRANSPORT` | `stdio` (`stdio` or `http`) |
| `--http-addr` | `TRACKER_HTTP_ADDR` | `127.0.0.1:8080` |
| `--database-url` | `DATABASE_URL` | `postgres://localhost:5432/tracker_mcp?sslmode=disable` |
| `--actor` | `TRACKER_ACTOR` | empty; recorded as `created_by` |
| `--max-db-connections` | `TRACKER_MAX_DB_CONNECTIONS` | `10` |
| `--statement-timeout` | `TRACKER_STATEMENT_TIMEOUT` | `15s` |
| `--log-level` | `TRACKER_LOG_LEVEL` | `info` (logs go to stderr) |

## Tools

| Area | Tools |
|---|---|
| Trackers | `create_tracker`, `list_trackers`, `describe_tracker`, `rename_tracker`, `delete_tracker`, `restore_tracker` |
| Columns | `add_column`, `update_column`, `rename_column`, `remove_column` |
| Rows | `add_rows`, `update_row`, `delete_rows`, `query_rows`, `summarize_rows` |

**Column types:** `text`, `number`, `boolean`, `date`, `datetime`, `single_select`, `multi_select`, `email`, `url`.
Friendly aliases are also accepted, such as `dropdown`, `checkbox`, `tags` and `link`.

**Filter operators:** `eq`, `neq`, `gt`, `gte`, `lt`, `lte`, `contains`, `not_contains`, `starts_with`, `in`, `not_in`, `is_empty`, `is_not_empty`.
Symbols such as `=`, `!=` and `>=` also work. Each column reports which operators it accepts in `describe_tracker`.

## Architecture

```
cmd/tracker-mcp          wiring: config → pool → migrations → services → MCP transport
internal/config          flags + env
internal/mcpserver       MCP adapter: DTOs, tool registration, error presentation (no business rules)
internal/service         business rules: name resolution, validation, previews, transactions
internal/domain          model + value rules (types, codecs, operators, suggestions); no I/O
internal/repository      persistence interfaces (Store, TrackerRepository, ColumnRepository, RowRepository)
internal/repository/postgres   pgx implementation + embedded SQL migrations
```

- **Dependencies point inwards.** `mcpserver` depends on service interfaces it defines itself. Services depend on `repository` interfaces, and only `main` knows about Postgres. Swapping the storage (e.g. to Mongo) means writing a new `repository.Store`.
- **Adding a column type** means adding a `valueCodec` in `internal/domain/values.go`. Query building works per `ValueKind`, so a new type that reuses an existing kind needs no SQL changes.
- **Storage** is metadata tables plus one JSONB document per row (`tracker_rows.data`).
  - Row values are keyed by the **column id**, not the column name, so renames are free.
  - User text never becomes a SQL identifier. Every key and value is a bind parameter.
  - A GIN `jsonb_path_ops` index serves exact-match filters.
- **Errors.** Domain errors are written for people and passed through to the client. Unexpected errors are logged and replaced with a generic message, so database internals don't leak.

## Edge cases handled

### Tracker identity
1. **Misspelled tracker name** ("Intervew Tracker"): the server never guesses. It returns "not found" with the closest matches found by `pg_trgm` similarity, each with id, creation date and row count, so the assistant can ask the user which one they meant.
2. **Same name, different creation time**: two *active* trackers can't share a name.
   - Names are compared ignoring case and extra whitespace (`"  interview   TRACKER"` equals `"Interview Tracker"`).
   - The error names the existing tracker and suggests a distinguishing name such as "Interview Tracker 2026".
   - A partial unique index also covers two creates racing each other.
3. **Look-alike names** ("interview" matching both "Interview Tracker" and "Interview Tracker 2025"): all candidates are listed. Every tool accepts `tracker_id` to disambiguate.
4. **Name and id disagree**: if both are given and they refer to different trackers, the call is rejected.
5. **Deleted trackers**:
   - Deletes are soft, and the error for a deleted tracker points to `restore_tracker`.
   - A deleted tracker frees its name.
   - Restoring by name when several deleted trackers share it asks for the id.
   - Restoring into a name that's now taken asks for `new_name`.

### Columns
6. **Unknown or misspelled columns** (in rows, filters, sorts): the error suggests the closest names and lists the available columns.
7. **Refused column names**: reserved names (`id`, `created_at`, `updated_at`, `created_by`), case-only duplicates, and more than 100 columns.
8. **Required columns on a tracker that already has rows** need a `default_value`, which backfills the existing rows. Making an existing column required fills empty rows from the default, or refuses and says how many rows are empty.
9. **Removing a select option still in use** is refused, with per-option row counts. A default that would stop being valid is also refused.
10. **Removing the last column** is refused. Changing a column's type isn't supported in v1, because it would need data conversion.

### Values
11. **Lenient but safe coercion**:
    - `"5"` and `"1,50,000"` become numbers. `yes`/`no` become booleans. `"selected"` becomes the canonical option `Selected`.
    - Multi-select values are de-duplicated.
    - Emails are lower-cased. URLs get `https://` added when the scheme is missing.
12. **Ambiguous dates** like `03/04/2026` are rejected rather than guessed. Datetimes without a timezone are UTC and are stored normalised to UTC.
13. **Empty values**: `""` and `[]` mean empty, and empty values are never stored. In `update_row`, `null` clears a value unless the column is required.

### Rows and queries
14. **Bulk add is all-or-nothing**, with every problem reported per row (capped at 200 rows per call).
15. **Updates are single-statement JSONB merges**, so concurrent edits to different fields of the same row don't overwrite each other.
16. **Row ids are scoped to their tracker**: an id from another tracker is "not found". `delete_rows` reports ids that don't exist.
17. **Destructive tools preview first**: `delete_tracker`, `remove_column` and `delete_rows` return a preview unless `confirm=true`.
18. **Negative filters include empty rows**: `neq`, `not_in` and `not_contains` also match rows where the field is empty.
19. **Invalid filters** are rejected with the allowed alternatives: an operator that doesn't fit the column type (`gt` on a select), or an unknown option in a filter.
20. **Literal matching**: `contains` treats `%` and `_` literally.
21. **Bounded results**: `query_rows` returns at most 500 rows with `total_matching`, `has_more` and `next_offset`. Sorting is stable (it always ends with `id`) and puts empty values last.
22. **Operational safety**:
    - Postgres `statement_timeout` guards against runaway queries.
    - Migrations use an advisory lock, so several instances can start at once.
    - The HTTP transport binds to loopback by default and logs a loud warning otherwise, because v1 has no auth.
    - Logs go to stderr so they never corrupt the stdio protocol.

## Not in v1 (candidates for next iterations)

- **Auth, ownership and sharing.** `created_by` and the `Actor` seam in `service` are where it plugs in. Optionally, workspaces per team so two teams can each have a "Tasks" tracker.
- **Richer bulk and schema operations:** bulk update or delete by filter (with preview), renaming a select option across rows, and changing a column type with conversion.
- **Unique columns** (e.g. prevent the same candidate email twice) and duplicate-row warnings.
- **Audit history and undo** for row edits.
- **Relative date filters** ("this week"). The assistant currently computes the dates itself.
