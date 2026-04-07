# git-issue-branch

Create git branches from GitHub issue numbers.

## Installation

```sh
go install github.com/josegonzalez/git-issue-branch@latest
```

Or build from source:

```sh
git clone https://github.com/josegonzalez/git-issue-branch.git
cd git-issue-branch
make install
```

## Usage

```sh
git issue-branch <issue-number>
```

Creates a branch named `{issue_number}-{hyphenated-title}` from the GitHub issue title. Semantic commit prefixes (e.g. `feat:`, `fix(scope):`) are stripped from the title.

### Flags

```text
-n, --dry-run  print branch name without creating it
-r, --remote   git remote to use (default "origin")
-t, --token    GitHub API token (overrides GITHUB_TOKEN)
-v, --version  print version
-h, --help     print help
```

### Authentication

Token resolution order:

1. `--token` flag
2. `GITHUB_TOKEN` environment variable
3. `~/.netrc` entry for `api.github.com`

No token is required for public repositories. For private repositories, provide a token with the `repo` scope.

## License

MIT
