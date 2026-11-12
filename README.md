# session-rg

Finds past Claude Code sessions by what was said in them. It searches the logs under `~/.claude/projects` with [ripgrep](https://github.com/BurntSushi/ripgrep) and prints each matching session's ID, starting folder, title, and the messages that matched.

```sh
session-rg handoff-probe                  # sessions where the word appears
session-rg herdr mod --since 3d           # every word must appear, in any messages of the session
session-rg --json --limit 5 '#15'         # JSON Lines for agents
```

- Only the conversation is searched: user messages (including `!` command output and the summary left by compaction) and Claude's replies. Tool input and output, thinking, text the harness inserts, and task notifications are not
- Case is ignored; words are plain strings, so Japanese works as is
- The session running the search (`$CLAUDE_CODE_SESSION_ID`) is left out
- Every log is read in full, so a session is found however long its last turns were
- `--since` takes a duration (`3d`, `12h`) or a date (`2026-11-01`) and compares it with the log's last write

Resume a result with `cd <cwd> && claude --resume <id>`.

## Install

With Nix (ripgrep comes with it):

```sh
nix profile install github:meltin/session-rg
```

Or add `github:meltin/session-rg` to a flake's inputs and use `packages.<system>.default`.

With Go (needs `rg` on your PATH):

```sh
go build -o ~/.local/bin/session-rg .   # anywhere on your PATH
```

## Test

```sh
go test ./...
```
