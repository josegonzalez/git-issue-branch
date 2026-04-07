package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"unicode"

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

func sanitizeBranchName(issueNumber int, title string) string {
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
	cleaned = strings.Trim(cleaned, "-")

	// Prepend issue number
	branch := fmt.Sprintf("%d-%s", issueNumber, cleaned)

	// Truncate to 240 characters
	if len(branch) > 240 {
		branch = branch[:240]
		// Trim trailing hyphen from truncation
		branch = strings.TrimRight(branch, "-")
	}

	return branch
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

func getDefaultBranch(remoteName string) string {
	// 1. Remote HEAD symref (set after clone)
	out, err := exec.Command("git", "symbolic-ref", fmt.Sprintf("refs/remotes/%s/HEAD", remoteName)).Output()
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

func resolveToken(flagToken string) string {
	if flagToken != "" {
		return flagToken
	}
	return os.Getenv("GITHUB_TOKEN")
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: git issue-branch <issue-number>\n\n")
		flag.PrintDefaults()
	}
	remote := flag.StringP("remote", "r", "origin", "git remote to use")
	token := flag.StringP("token", "t", "", "GitHub API token (overrides GITHUB_TOKEN env var)")
	dryRun := flag.BoolP("dry-run", "n", false, "print branch name without creating it")
	version := flag.BoolP("version", "v", false, "print version")
	flag.Parse()

	if *version {
		fmt.Println(Version)
		os.Exit(0)
	}

	args := flag.Args()
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: git-issue-branch <issue-number>")
		os.Exit(1)
	}

	issueNumber, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid issue number: %s\n", args[0])
		os.Exit(1)
	}

	remoteURL, err := getRemoteURL(*remote)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	owner, repo, err := parseGitRemote(remoteURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Try without auth first; retry with token on failure
	title, err := fetchIssue("https://api.github.com", owner, repo, issueNumber, "")
	if err != nil {
		authToken := resolveToken(*token)
		if authToken != "" {
			title, err = fetchIssue("https://api.github.com", owner, repo, issueNumber, authToken)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	branch := sanitizeBranchName(issueNumber, title)

	if *dryRun {
		fmt.Println(branch)
		return
	}

	defaultBranch := getDefaultBranch(*remote)
	cmd := exec.Command("git", "checkout", "-b", branch, defaultBranch)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}
}
