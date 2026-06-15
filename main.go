package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"
	netrc "github.com/jdx/go-netrc"
	flag "github.com/spf13/pflag"
)

// Version is set via ldflags at build time.
var Version = "dev"

var semanticPrefixRe = regexp.MustCompile(`^(?:feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)(?:\([^)]*\))?:\s*`)
var invalidCharsRe = regexp.MustCompile(`[^a-z0-9-]+`)
var multiDashRe = regexp.MustCompile(`-{2,}`)

func stripSemanticPrefix(title string) string {
	return semanticPrefixRe.ReplaceAllString(title, "")
}

// sanitizeTitle normalizes an issue title into the hyphenated portion of a
// branch name. It strips any semantic commit prefix, lowercases, drops
// non-ASCII characters, replaces invalid git branch characters with hyphens,
// collapses runs of hyphens, and trims leading/trailing hyphens. It does not
// add a prefix or truncate.
func sanitizeTitle(title string) string {
	title = stripSemanticPrefix(title)
	title = strings.ToLower(title)

	// Remove non-ASCII characters
	cleaned := strings.Map(func(r rune) rune {
		if r > unicode.MaxASCII {
			return -1
		}
		return r
	}, title)

	// Replace invalid git branch characters with hyphens
	cleaned = invalidCharsRe.ReplaceAllString(cleaned, "-")

	// Collapse consecutive hyphens
	cleaned = multiDashRe.ReplaceAllString(cleaned, "-")

	// Trim leading/trailing hyphens
	return strings.Trim(cleaned, "-")
}

// truncateBranch caps a branch name at 240 characters, trimming any trailing
// hyphen or slash left behind by truncation (a trailing slash is invalid in a
// git ref).
func truncateBranch(branch string) string {
	if len(branch) > 240 {
		branch = branch[:240]
		branch = strings.TrimRight(branch, "-/")
	}
	return branch
}

func sanitizeBranchName(issueNumber int, title string) string {
	return truncateBranch(fmt.Sprintf("%d-%s", issueNumber, sanitizeTitle(title)))
}

// linearBranchName builds a Linear branch name of the form
// "username/eng-123/title". The username segment is omitted when empty. The
// identifier is lowercased; the username is lightly sanitized.
func linearBranchName(username, identifier, title string) string {
	var segments []string
	if u := sanitizeSegment(username); u != "" {
		segments = append(segments, u)
	}
	segments = append(segments, strings.ToLower(identifier), sanitizeTitle(title))
	return truncateBranch(strings.Join(segments, "/"))
}

// sanitizeSegment normalizes a single path segment (e.g. the username) into
// lowercase ASCII with invalid characters replaced by hyphens.
func sanitizeSegment(segment string) string {
	segment = strings.ToLower(segment)
	cleaned := strings.Map(func(r rune) rune {
		if r > unicode.MaxASCII {
			return -1
		}
		return r
	}, segment)
	cleaned = invalidCharsRe.ReplaceAllString(cleaned, "-")
	cleaned = multiDashRe.ReplaceAllString(cleaned, "-")
	return strings.Trim(cleaned, "-")
}

var linearIdentRe = regexp.MustCompile(`^[A-Za-z]+-\d+$`)
var linearURLRe = regexp.MustCompile(`^https?://linear\.app/[^/]+/issue/([A-Za-z]+-\d+)`)

type issueProvider int

const (
	providerGitHub issueProvider = iota
	providerLinear
)

// detectProvider classifies the positional argument. A pure integer is a
// GitHub issue number; a linear.app issue URL or a bare identifier like
// ENG-123 is a Linear ticket.
func detectProvider(arg string) (issueProvider, error) {
	if linearURLRe.MatchString(arg) {
		return providerLinear, nil
	}
	if _, err := strconv.Atoi(arg); err == nil {
		return providerGitHub, nil
	}
	if linearIdentRe.MatchString(arg) {
		return providerLinear, nil
	}
	return 0, fmt.Errorf("unrecognized issue reference: %s", arg)
}

// parseLinearIdentifier extracts the canonical, uppercased identifier (e.g.
// ENG-123) from either a linear.app issue URL or a bare identifier.
func parseLinearIdentifier(arg string) (string, error) {
	if m := linearURLRe.FindStringSubmatch(arg); m != nil {
		return strings.ToUpper(m[1]), nil
	}
	if linearIdentRe.MatchString(arg) {
		return strings.ToUpper(arg), nil
	}
	return "", fmt.Errorf("invalid Linear identifier: %s", arg)
}

func parseGitRemote(remoteURL string) (string, string, error) {
	// SSH: git@github.com:owner/repo.git
	sshRe := regexp.MustCompile(`^git@[^:]+:([^/]+)/([^/.]+?)(?:\.git)?$`)
	if m := sshRe.FindStringSubmatch(remoteURL); m != nil {
		return m[1], m[2], nil
	}

	// HTTPS: https://github.com/owner/repo.git
	httpsRe := regexp.MustCompile(`^https?://[^/]+/([^/]+)/([^/.]+?)(?:\.git)?$`)
	if m := httpsRe.FindStringSubmatch(remoteURL); m != nil {
		return m[1], m[2], nil
	}

	return "", "", fmt.Errorf("unable to parse remote URL: %s", remoteURL)
}

func getRemoteURL(remoteName string) (string, error) {
	out, err := exec.Command("git", "remote", "get-url", remoteName).Output()
	if err != nil {
		return "", fmt.Errorf("failed to get remote URL for %q: %w", remoteName, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func fetchIssue(baseURL, owner, repo string, number int, token string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d", baseURL, owner, repo, number)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch issue: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned status %d for issue #%d", resp.StatusCode, number)
	}

	var issue struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&issue); err != nil {
		return "", fmt.Errorf("failed to parse issue response: %w", err)
	}

	return issue.Title, nil
}

type linearIssue struct {
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
}

// fetchLinearIssue queries the Linear GraphQL API for an issue by its
// human-readable identifier (e.g. ENG-123). Linear returns errors in a JSON
// "errors" array even with an HTTP 200 status, so those are checked explicitly.
func fetchLinearIssue(baseURL, identifier, token string) (linearIssue, error) {
	body, err := json.Marshal(map[string]any{
		"query":     "query($id:String!){issue(id:$id){identifier title}}",
		"variables": map[string]string{"id": identifier},
	})
	if err != nil {
		return linearIssue{}, err
	}

	req, err := http.NewRequest("POST", baseURL, bytes.NewReader(body))
	if err != nil {
		return linearIssue{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	// Personal API keys are sent without a "Bearer" prefix.
	req.Header.Set("Authorization", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return linearIssue{}, fmt.Errorf("failed to fetch Linear issue: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return linearIssue{}, fmt.Errorf("Linear API returned status %d for issue %s", resp.StatusCode, identifier)
	}

	var out struct {
		Data struct {
			Issue *linearIssue `json:"issue"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return linearIssue{}, fmt.Errorf("failed to parse Linear response: %w", err)
	}

	if len(out.Errors) > 0 {
		return linearIssue{}, fmt.Errorf("Linear API error: %s", out.Errors[0].Message)
	}
	if out.Data.Issue == nil {
		return linearIssue{}, fmt.Errorf("Linear issue %s not found", identifier)
	}
	return *out.Data.Issue, nil
}

func getDefaultBranch(remoteName string) string {
	// 1. Query the remote directly for its HEAD (avoids stale local symrefs)
	out, err := exec.Command("git", "ls-remote", "--symref", remoteName, "HEAD").Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "ref: refs/heads/") {
				ref := strings.TrimPrefix(line, "ref: refs/heads/")
				if i := strings.Index(ref, "\t"); i >= 0 {
					return ref[:i]
				}
			}
		}
	}

	// 2. Fall back to local symref (works offline)
	out, err = exec.Command("git", "symbolic-ref", fmt.Sprintf("refs/remotes/%s/HEAD", remoteName)).Output()
	if err == nil {
		ref := strings.TrimSpace(string(out))
		if i := strings.LastIndex(ref, "/"); i >= 0 {
			return ref[i+1:]
		}
	}

	// 2. Git config init.defaultBranch
	out, err = exec.Command("git", "config", "init.defaultBranch").Output()
	if err == nil {
		if branch := strings.TrimSpace(string(out)); branch != "" {
			return branch
		}
	}

	// 3. Check if main or master exist locally
	for _, branch := range []string{"main", "master"} {
		if exec.Command("git", "rev-parse", "--verify", branch).Run() == nil {
			return branch
		}
	}

	return "main"
}

func resolveBaseRef(remoteName, baseBranch string) (string, error) {
	if baseBranch != "" {
		remoteRef := remoteName + "/" + baseBranch
		if exec.Command("git", "rev-parse", "--verify", remoteRef).Run() == nil {
			return remoteRef, nil
		}
		if exec.Command("git", "rev-parse", "--verify", baseBranch).Run() == nil {
			return baseBranch, nil
		}
		return "", fmt.Errorf("base branch %q not found (tried %q and %q)", baseBranch, remoteRef, baseBranch)
	}

	defaultBranch := getDefaultBranch(remoteName)
	remoteRef := remoteName + "/" + defaultBranch
	if exec.Command("git", "rev-parse", "--verify", remoteRef).Run() == nil {
		return remoteRef, nil
	}
	if exec.Command("git", "rev-parse", "--verify", defaultBranch).Run() == nil {
		return defaultBranch, nil
	}
	for _, fallback := range []string{"main", "master"} {
		ref := remoteName + "/" + fallback
		if exec.Command("git", "rev-parse", "--verify", ref).Run() == nil {
			return ref, nil
		}
	}
	return "", fmt.Errorf("could not determine base branch; use --base to specify one")
}

var ghAuthToken = func() string {
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

var netrcToken = func() string {
	return netrcTokenFromFile(defaultNetrcPath())
}

func defaultNetrcPath() string {
	if p := os.Getenv("NETRC"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".netrc")
}

func netrcTokenFromFile(path string) string {
	if path == "" {
		return ""
	}
	n, err := netrc.Parse(path)
	if err != nil {
		return ""
	}
	if m := n.Machine("api.github.com"); m != nil {
		return m.Get("password")
	}
	return ""
}

func resolveToken(flagToken string) string {
	if flagToken != "" {
		return flagToken
	}
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t
	}
	if t := os.Getenv("GH_TOKEN"); t != "" {
		return t
	}
	if t := netrcToken(); t != "" {
		return t
	}
	return ghAuthToken()
}

func configDir() string {
	if p := os.Getenv("XDG_CONFIG_HOME"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config")
}

// linearTokenKeys are the candidate field names that the various Linear CLIs
// use to store an API token in their config files.
var linearTokenKeys = []string{"token", "api_key", "apiKey", "access_token"}

func tokenFromMap(m map[string]any) string {
	for _, key := range linearTokenKeys {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// linearTokenFromPlainFile reads a token stored as the whole contents of a
// file (the joa23 Go CLI stores its access token in ~/.config/linear/token).
func linearTokenFromPlainFile(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func linearTokenFromJSON(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return ""
	}
	return tokenFromMap(m)
}

func linearTokenFromTOML(path string) string {
	if path == "" {
		return ""
	}
	var m map[string]any
	if _, err := toml.DecodeFile(path, &m); err != nil {
		return ""
	}
	return tokenFromMap(m)
}

// Package-level vars so tests can stub each source independently.
var linearPlainFileToken = func() string {
	return linearTokenFromPlainFile(filepath.Join(configDir(), "linear", "token"))
}

var linearTOMLToken = func() string {
	return linearTokenFromTOML(filepath.Join(configDir(), "linear-cli", "config.toml"))
}

var linearJSONToken = func() string {
	return linearTokenFromJSON(filepath.Join(configDir(), "linear", "config.json"))
}

// linearCLIToken asks schpet's `linear` CLI to print its resolved API key,
// which it reads from the OS keyring (mirrors the `gh auth token` fallback).
var linearCLIToken = func() string {
	out, err := exec.Command("linear", "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func resolveLinearToken(flagToken string) string {
	if flagToken != "" {
		return flagToken
	}
	if t := os.Getenv("LINEAR_API_KEY"); t != "" {
		return t
	}
	if t := linearPlainFileToken(); t != "" {
		return t
	}
	if t := linearTOMLToken(); t != "" {
		return t
	}
	if t := linearJSONToken(); t != "" {
		return t
	}
	return linearCLIToken()
}

func gitConfigValue(key string) string {
	out, err := exec.Command("git", "config", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// resolveLinearUsername determines the username segment for a Linear branch
// name, preferring an explicit gitconfig key, then the GitHub username, then
// the $USER environment variable.
func resolveLinearUsername() string {
	if u := gitConfigValue("issue-branch.username"); u != "" {
		return u
	}
	if u := gitConfigValue("github.user"); u != "" {
		return u
	}
	return strings.TrimSpace(os.Getenv("USER"))
}

// resolveGitHubBranch fetches a GitHub issue title and returns its branch name,
// exiting on failure.
func resolveGitHubBranch(arg, remote, flagToken string) string {
	issueNumber, err := strconv.Atoi(arg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid issue number: %s\n", arg)
		os.Exit(1)
	}

	remoteURL, err := getRemoteURL(remote)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	owner, repo, err := parseGitRemote(remoteURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Try without auth first; retry with token on failure.
	title, err := fetchIssue("https://api.github.com", owner, repo, issueNumber, "")
	if err != nil {
		if authToken := resolveToken(flagToken); authToken != "" {
			title, err = fetchIssue("https://api.github.com", owner, repo, issueNumber, authToken)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	return sanitizeBranchName(issueNumber, title)
}

// resolveLinearBranch fetches a Linear issue and returns its branch name,
// exiting on failure.
func resolveLinearBranch(arg, flagToken string) string {
	identifier, err := parseLinearIdentifier(arg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	token := resolveLinearToken(flagToken)
	if token == "" {
		fmt.Fprintln(os.Stderr, "no Linear API key found; set --linear-token or LINEAR_API_KEY")
		os.Exit(1)
	}

	issue, err := fetchLinearIssue("https://api.linear.app/graphql", identifier, token)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	return linearBranchName(resolveLinearUsername(), issue.Identifier, issue.Title)
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: git issue-branch <issue-number | LINEAR-ID | linear-url>\n\n")
		flag.PrintDefaults()
	}
	remote := flag.StringP("remote", "r", "origin", "git remote to use")
	base := flag.StringP("base", "b", "", "base branch to create the new branch from")
	token := flag.StringP("gh-token", "t", "", "GitHub API token (overrides GITHUB_TOKEN env var)")
	linearToken := flag.String("linear-token", "", "Linear API key (overrides LINEAR_API_KEY env var)")
	dryRun := flag.BoolP("dry-run", "n", false, "print branch name without creating it")
	version := flag.BoolP("version", "v", false, "print version")
	flag.Parse()

	if *version {
		fmt.Println(Version)
		os.Exit(0)
	}

	args := flag.Args()
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: git-issue-branch <issue-number | LINEAR-ID | linear-url>")
		os.Exit(1)
	}

	provider, err := detectProvider(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var branch string
	if provider == providerLinear {
		branch = resolveLinearBranch(args[0], *linearToken)
	} else {
		branch = resolveGitHubBranch(args[0], *remote, *token)
	}

	if *dryRun {
		fmt.Println(branch)
		return
	}

	baseRef, err := resolveBaseRef(*remote, *base)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cmd := exec.Command("git", "checkout", "-b", branch, "--no-track", baseRef)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}
}
