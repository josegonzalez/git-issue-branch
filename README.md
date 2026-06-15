# git-issue-branch

Create git branches from GitHub issue numbers or Linear tickets.

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
git issue-branch <issue-number | LINEAR-ID | linear-url>
```

The provider is auto-detected from the argument:

- A plain integer (e.g. `42`) is a GitHub issue. Creates a branch named `{issue_number}-{hyphenated-title}` from the GitHub issue title.
- A Linear identifier (e.g. `ENG-123`) or a Linear issue URL (e.g. `https://linear.app/acme/issue/ENG-123/some-slug`) is a Linear ticket. Creates a branch named `{username}/{identifier}/{hyphenated-title}`.

Semantic commit prefixes (e.g. `feat:`, `fix(scope):`) are stripped from the title.

### Flags

```text
-b, --base          base branch to create from (default: auto-detected)
-n, --dry-run       print branch name without creating it
-r, --remote        git remote to use (default "origin")
-t, --gh-token      GitHub API token (overrides GITHUB_TOKEN)
    --linear-token  Linear API key (overrides LINEAR_API_KEY)
-v, --version       print version
-h, --help          print help
```

### GitHub authentication

Token resolution order:

1. `--gh-token` flag
2. `GITHUB_TOKEN` environment variable
3. `GH_TOKEN` environment variable
4. `~/.netrc` entry for `api.github.com` (token in the `password` field)
5. `gh auth token` (the `gh` CLI's stored credential, if logged in via `gh`)

No token is required for public repositories. For private repositories, provide a token with the `repo` scope, which is satisfied automatically via `~/.netrc` or when logged in via `gh`.

### Linear authentication

Linear has no anonymous access, so an API key is always required. Personal API keys can be created in Linear under Settings → Security & access. Key resolution order:

1. `--linear-token` flag
2. `LINEAR_API_KEY` environment variable
3. `~/.config/linear/token` (the joa23 Go CLI's stored access token)
4. `~/.config/linear-cli/config.toml` (the Finesssee Rust CLI's `api_key`)
5. `~/.config/linear/config.json` (a `token`/`api_key` entry)
6. `linear auth token` (schpet's `linear` CLI, which prints the key it stores in the OS keyring)

(`$XDG_CONFIG_HOME` is honored when set.)

### Linear branch username

The username segment of a Linear branch name is resolved in this order:

1. gitconfig `issue-branch.username` (e.g. `git config --global issue-branch.username jose`)
2. gitconfig `github.user`
3. the `$USER` environment variable

If none is set, the username segment is omitted (the branch becomes `{identifier}/{title}`).

## License

MIT
