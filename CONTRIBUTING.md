# Contributing

## Setup

```sh
git clone git@github.com:fishman/notmutt.git
cd notmutt/src
go build -o ../notmutt .
```

Requirements and the build variants (`-tags cli`, `-tags lua`) are in
[docs/installation.md](docs/installation.md). Requirements and
architecture are normative in [AGENTS.md](AGENTS.md); the security
model lives in [SECURITY.md](SECURITY.md).

## Commits

Commits follow Conventional Commits: `type(scope): subject`, brief
lowercase imperative subject.

- All code is owned by its author: code commits carry no AI marker and
  no co-author line, whether or not an AI drafted them (the README
  explains the rule).
- Doc and spec commits carry `Co-Authored-By: Deepseek` (the model
  that drafted them). Review responsibility stays with the author.

## Gates

Every change must pass, before push:

```sh
cd src
gofmt -l .            # empty, excluding vendor
go vet ./...
go test ./... -count=1
go build -tags lua ./...
go test -tags lua ./app/
```

## Test data

Fabricated only: alpha, atlas, acme, sender@example.com. Real account
names and real people's addresses never appear in tests, issues, or
commits.

## Privacy

Never include mail content (bodies, headers, whole .eml/.mbox files)
in issues, PRs, or commits. Reproduce message-specific bugs with
fabricated mail; when correlating a message, cite a checksum
(sha256) of the file, not its content. Config files are not mail
content and may be shared with secrets removed.
