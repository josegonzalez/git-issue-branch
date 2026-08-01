package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestStripSemanticPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"feat: add login page", "add login page"},
		{"fix(auth): resolve token expiry", "resolve token expiry"},
		{"docs: update readme", "update readme"},
		{"refactor(core): simplify logic", "simplify logic"},
		{"chore: bump deps", "bump deps"},
		{"revert: undo change", "undo change"},
		{"no prefix here", "no prefix here"},
		{"feature: not a valid prefix", "feature: not a valid prefix"},
		{"fix:no space", "no space"},
		{"build(deps): update module", "update module"},
		{"ci: add workflow", "add workflow"},
		{"perf: optimize query", "optimize query"},
		{"style: format code", "format code"},
		{"test: add coverage", "add coverage"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := stripSemanticPrefix(tt.input)
			if got != tt.want {
				t.Errorf("stripSemanticPrefix(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitizeBranchName(t *testing.T) {
	tests := []struct {
		name        string
		issueNumber int
		title       string
		want        string
	}{
		{
			name:        "basic with feat prefix",
			issueNumber: 42,
			title:       "feat: add login page",
			want:        "42-add-login-page",
		},
		{
			name:        "scoped prefix",
			issueNumber: 123,
			title:       "fix(auth): resolve token expiry",
			want:        "123-resolve-token-expiry",
		},
		{
			name:        "no prefix",
			issueNumber: 7,
			title:       "Simple title",
			want:        "7-simple-title",
		},
		{
			name:        "extra spaces",
			issueNumber: 1,
			title:       "Lots   of   spaces",
			want:        "1-lots-of-spaces",
		},
		{
			name:        "special characters",
			issueNumber: 99,
			title:       "chore: remove $$pecial ch@rs!",
			want:        "99-remove-pecial-ch-rs",
		},
		{
			name:        "consecutive dashes in title",
			issueNumber: 10,
			title:       "fix: --double--dashes--",
			want:        "10-double-dashes",
		},
		{
			name:        "unicode characters stripped",
			issueNumber: 5,
			title:       "feat: cool feature",
			want:        "5-cool-feature",
		},
		{
			name:        "truncation at 240 chars",
			issueNumber: 1,
			title:       "feat: " + strings.Repeat("a-", 200),
			want: func() string {
				branch := "1-" + strings.Repeat("a-", 200)
				if len(branch) > 240 {
					branch = branch[:240]
					branch = strings.TrimRight(branch, "-")
				}
				return branch
			}(),
		},
		{
			name:        "dots and slashes",
			issueNumber: 15,
			title:       "fix: path/to/file.go issue",
			want:        "15-path-to-file-go-issue",
		},
		{
			name:        "brackets and parens",
			issueNumber: 20,
			title:       "feat: add [new] (feature)",
			want:        "20-add-new-feature",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeBranchName(tt.issueNumber, tt.title)
			if got != tt.want {
				t.Errorf("sanitizeBranchName(%d, %q) = %q, want %q", tt.issueNumber, tt.title, got, tt.want)
			}

			// Verify branch name invariants
			if len(got) > 240 {
				t.Errorf("branch name exceeds 240 chars: len=%d", len(got))
			}
			if strings.Contains(got, "--") {
				t.Errorf("branch name contains consecutive dashes: %q", got)
			}
			if strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") {
				t.Errorf("branch name has leading/trailing dash: %q", got)
			}
		})
	}
}

func TestSecurityBranchName(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		number int
		title  string
		want   string
	}{
		{
			name:   "dependabot summary",
			prefix: "dependabot",
			number: 20,
			title:  "Ruby json: JSON generator heap buffer overflow when streaming to an IO",
			want:   "dependabot-20-ruby-json-json-generator-heap-buffer-overflow-when-streaming-to-an-io",
		},
		{
			name:   "code-scanning description",
			prefix: "code-scanning",
			number: 1,
			title:  "Size computation for allocation may overflow",
			want:   "code-scanning-1-size-computation-for-allocation-may-overflow",
		},
		{
			name:   "empty title omits trailing hyphen",
			prefix: "dependabot",
			number: 5,
			title:  "",
			want:   "dependabot-5",
		},
		{
			name:   "title sanitizing to empty omits segment",
			prefix: "code-scanning",
			number: 7,
			title:  "!!!",
			want:   "code-scanning-7",
		},
		{
			name:   "long title truncated to 240",
			prefix: "dependabot",
			number: 1,
			title:  strings.Repeat("a-", 200),
			want: func() string {
				branch := "dependabot-1-" + strings.Repeat("a-", 200)
				branch = strings.TrimRight(branch, "-")
				if len(branch) > 240 {
					branch = branch[:240]
					branch = strings.TrimRight(branch, "-/")
				}
				return branch
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := securityBranchName(tt.prefix, tt.number, tt.title)
			if got != tt.want {
				t.Errorf("securityBranchName(%q, %d, %q) = %q, want %q", tt.prefix, tt.number, tt.title, got, tt.want)
			}

			// Verify branch name invariants
			if len(got) > 240 {
				t.Errorf("branch name exceeds 240 chars: len=%d", len(got))
			}
			if strings.Contains(got, "--") {
				t.Errorf("branch name contains consecutive dashes: %q", got)
			}
			if strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") {
				t.Errorf("branch name has leading/trailing dash: %q", got)
			}
		})
	}
}

func TestParseGitRemote(t *testing.T) {
	tests := []struct {
		name      string
		remoteURL string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		{
			name:      "SSH with .git",
			remoteURL: "git@github.com:josegonzalez/git-issue-branch.git",
			wantOwner: "josegonzalez",
			wantRepo:  "git-issue-branch",
		},
		{
			name:      "SSH without .git",
			remoteURL: "git@github.com:josegonzalez/git-issue-branch",
			wantOwner: "josegonzalez",
			wantRepo:  "git-issue-branch",
		},
		{
			name:      "HTTPS with .git",
			remoteURL: "https://github.com/josegonzalez/git-issue-branch.git",
			wantOwner: "josegonzalez",
			wantRepo:  "git-issue-branch",
		},
		{
			name:      "HTTPS without .git",
			remoteURL: "https://github.com/josegonzalez/git-issue-branch",
			wantOwner: "josegonzalez",
			wantRepo:  "git-issue-branch",
		},
		{
			name:      "HTTP",
			remoteURL: "http://github.com/owner/repo.git",
			wantOwner: "owner",
			wantRepo:  "repo",
		},
		{
			name:      "invalid URL",
			remoteURL: "not-a-url",
			wantErr:   true,
		},
		{
			name:      "empty string",
			remoteURL: "",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, err := parseGitRemote(tt.remoteURL)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseGitRemote(%q) error = %v, wantErr %v", tt.remoteURL, err, tt.wantErr)
				return
			}
			if owner != tt.wantOwner {
				t.Errorf("parseGitRemote(%q) owner = %q, want %q", tt.remoteURL, owner, tt.wantOwner)
			}
			if repo != tt.wantRepo {
				t.Errorf("parseGitRemote(%q) repo = %q, want %q", tt.remoteURL, repo, tt.wantRepo)
			}
		})
	}
}

func TestFetchIssue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/issues/42" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"number": 42, "title": "feat: add login page"}`)
	}))
	defer server.Close()

	title, err := fetchIssue(server.URL, "owner", "repo", 42, "")
	if err != nil {
		t.Fatalf("fetchIssue() error = %v", err)
	}
	if title != "feat: add login page" {
		t.Errorf("fetchIssue() title = %q, want %q", title, "feat: add login page")
	}
}

func TestFetchIssueNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	_, err := fetchIssue(server.URL, "owner", "repo", 999, "")
	if err == nil {
		t.Fatal("fetchIssue() expected error for 404, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("fetchIssue() error = %q, want error containing '404'", err)
	}
}

func TestFetchIssueWithToken(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"number": 1, "title": "test issue"}`)
	}))
	defer server.Close()

	_, err := fetchIssue(server.URL, "owner", "repo", 1, "ghp_test123")
	if err != nil {
		t.Fatalf("fetchIssue() error = %v", err)
	}
	if gotAuth != "token ghp_test123" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "token ghp_test123")
	}
}

func TestFetchIssueWithoutToken(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"number": 1, "title": "test issue"}`)
	}))
	defer server.Close()

	_, err := fetchIssue(server.URL, "owner", "repo", 1, "")
	if err != nil {
		t.Fatalf("fetchIssue() error = %v", err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization header = %q, want empty", gotAuth)
	}
}

func TestFetchIssueRetryWithToken(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"number": 1, "title": "private issue"}`)
	}))
	defer server.Close()

	// First call without token should fail
	_, err := fetchIssue(server.URL, "owner", "repo", 1, "")
	if err == nil {
		t.Fatal("expected error without token")
	}

	// Retry with token should succeed
	title, err := fetchIssue(server.URL, "owner", "repo", 1, "ghp_secret")
	if err != nil {
		t.Fatalf("fetchIssue() with token error = %v", err)
	}
	if title != "private issue" {
		t.Errorf("fetchIssue() title = %q, want %q", title, "private issue")
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
}

func TestFetchDependabotAlert(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/dependabot/alerts/20" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"number":20,"security_advisory":{"summary":"Ruby json: JSON generator heap buffer overflow when streaming to an IO"}}`)
	}))
	defer server.Close()

	summary, err := fetchDependabotAlert(server.URL, "owner", "repo", 20, "ghp_test123")
	if err != nil {
		t.Fatalf("fetchDependabotAlert() error = %v", err)
	}
	if want := "Ruby json: JSON generator heap buffer overflow when streaming to an IO"; summary != want {
		t.Errorf("fetchDependabotAlert() summary = %q, want %q", summary, want)
	}
	if gotAuth != "token ghp_test123" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "token ghp_test123")
	}
}

func TestFetchDependabotAlertNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	_, err := fetchDependabotAlert(server.URL, "owner", "repo", 999, "")
	if err == nil {
		t.Fatal("fetchDependabotAlert() expected error for 404, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("fetchDependabotAlert() error = %q, want error containing '404'", err)
	}
}

func TestFetchCodeScanningAlert(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/code-scanning/alerts/1" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"number":1,"rule":{"id":"go/allocation-size-overflow","description":"Size computation for allocation may overflow"}}`)
	}))
	defer server.Close()

	description, err := fetchCodeScanningAlert(server.URL, "owner", "repo", 1, "ghp_test123")
	if err != nil {
		t.Fatalf("fetchCodeScanningAlert() error = %v", err)
	}
	if want := "Size computation for allocation may overflow"; description != want {
		t.Errorf("fetchCodeScanningAlert() description = %q, want %q", description, want)
	}
	if gotAuth != "token ghp_test123" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "token ghp_test123")
	}
}

func TestFetchCodeScanningAlertNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	_, err := fetchCodeScanningAlert(server.URL, "owner", "repo", 999, "")
	if err == nil {
		t.Fatal("fetchCodeScanningAlert() expected error for 404, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("fetchCodeScanningAlert() error = %q, want error containing '404'", err)
	}
}

func setupTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmds := [][]string{
		{"git", "init", "--initial-branch=main", dir},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "commit", "--allow-empty", "-m", "init"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestGetDefaultBranchFromSymref(t *testing.T) {
	dir := setupTestRepo(t)
	// Create a bare "remote" and add it as origin
	remoteDir := t.TempDir()
	exec.Command("git", "clone", "--bare", dir, remoteDir).Run()

	cmd := exec.Command("git", "remote", "add", "origin", remoteDir)
	cmd.Dir = dir
	cmd.Run()

	// Fetch to populate remote refs, then set remote HEAD
	exec.Command("git", "fetch", "origin").Run()
	cmd = exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	cmd.Dir = dir
	cmd.Run()

	// Save and restore working directory
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	got := getDefaultBranch("origin")
	if got != "main" {
		t.Errorf("getDefaultBranch() = %q, want %q", got, "main")
	}
}

func TestGetDefaultBranchFromConfig(t *testing.T) {
	dir := setupTestRepo(t)

	cmd := exec.Command("git", "config", "init.defaultBranch", "develop")
	cmd.Dir = dir
	cmd.Run()

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	got := getDefaultBranch("origin")
	if got != "develop" {
		t.Errorf("getDefaultBranch() = %q, want %q", got, "develop")
	}
}

func TestGetDefaultBranchFallbackMain(t *testing.T) {
	dir := setupTestRepo(t)

	// Unset init.defaultBranch to ensure fallback
	cmd := exec.Command("git", "config", "--unset", "init.defaultBranch")
	cmd.Dir = dir
	cmd.Run()

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	got := getDefaultBranch("origin")
	if got != "main" {
		t.Errorf("getDefaultBranch() = %q, want %q", got, "main")
	}
}

func setupTestRepoWithRemote(t *testing.T) string {
	t.Helper()
	dir := setupTestRepo(t)
	remoteDir := t.TempDir()

	cmd := exec.Command("git", "clone", "--bare", dir, remoteDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bare clone: %v\n%s", err, out)
	}

	cmd = exec.Command("git", "remote", "add", "origin", remoteDir)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("add remote: %v\n%s", err, out)
	}

	cmd = exec.Command("git", "fetch", "origin")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fetch: %v\n%s", err, out)
	}

	return dir
}

func TestResolveBaseRefUsesRemoteQualified(t *testing.T) {
	dir := setupTestRepoWithRemote(t)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	ref, err := resolveBaseRef("origin", "")
	if err != nil {
		t.Fatalf("resolveBaseRef() error = %v", err)
	}
	if ref != "origin/main" {
		t.Errorf("resolveBaseRef() = %q, want %q", ref, "origin/main")
	}
}

func TestResolveBaseRefNoLocalBranch(t *testing.T) {
	dir := setupTestRepoWithRemote(t)

	// Delete local main branch (detach HEAD first)
	cmd := exec.Command("git", "checkout", "--detach")
	cmd.Dir = dir
	cmd.Run()
	cmd = exec.Command("git", "branch", "-D", "main")
	cmd.Dir = dir
	cmd.Run()

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	ref, err := resolveBaseRef("origin", "")
	if err != nil {
		t.Fatalf("resolveBaseRef() error = %v", err)
	}
	if ref != "origin/main" {
		t.Errorf("resolveBaseRef() = %q, want %q", ref, "origin/main")
	}
}

func TestResolveBaseRefWithExplicitBase(t *testing.T) {
	dir := setupTestRepoWithRemote(t)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	ref, err := resolveBaseRef("origin", "main")
	if err != nil {
		t.Fatalf("resolveBaseRef() error = %v", err)
	}
	if ref != "origin/main" {
		t.Errorf("resolveBaseRef() = %q, want %q", ref, "origin/main")
	}
}

func TestResolveBaseRefInvalidBase(t *testing.T) {
	dir := setupTestRepoWithRemote(t)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	_, err := resolveBaseRef("origin", "nonexistent")
	if err == nil {
		t.Fatal("resolveBaseRef() expected error for nonexistent base, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("resolveBaseRef() error = %q, want error containing 'not found'", err)
	}
}

func TestResolveBaseRefFallbackLocal(t *testing.T) {
	dir := setupTestRepo(t)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	// No remote configured, should fall back to local "main"
	ref, err := resolveBaseRef("origin", "")
	if err != nil {
		t.Fatalf("resolveBaseRef() error = %v", err)
	}
	if ref != "main" {
		t.Errorf("resolveBaseRef() = %q, want %q", ref, "main")
	}
}

func TestCheckoutNoTrack(t *testing.T) {
	dir := setupTestRepoWithRemote(t)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	// Create a branch the same way main.go does: git checkout -b <branch> --no-track <baseRef>
	cmd := exec.Command("git", "checkout", "-b", "42-test-feature", "--no-track", "origin/main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("checkout: %v\n%s", err, out)
	}

	// Verify no upstream tracking is configured
	cmd = exec.Command("git", "config", "branch.42-test-feature.remote")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected no tracking remote, but got %q", strings.TrimSpace(string(out)))
	}

	cmd = exec.Command("git", "config", "branch.42-test-feature.merge")
	cmd.Dir = dir
	out, err = cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected no tracking merge ref, but got %q", strings.TrimSpace(string(out)))
	}
}

func TestResolveToken(t *testing.T) {
	// Stub netrc and gh lookups so precedence is tested in isolation.
	origNetrc, origGH := netrcToken, ghAuthToken
	defer func() { netrcToken, ghAuthToken = origNetrc, origGH }()
	netrcToken = func() string { return "" }
	ghAuthToken = func() string { return "" }

	// Flag takes priority
	got := resolveToken("flag-token")
	if got != "flag-token" {
		t.Errorf("resolveToken() = %q, want %q", got, "flag-token")
	}

	// Falls back to GITHUB_TOKEN
	t.Setenv("GITHUB_TOKEN", "env-token")
	got = resolveToken("")
	if got != "env-token" {
		t.Errorf("resolveToken() = %q, want %q", got, "env-token")
	}

	// Falls back to GH_TOKEN ahead of netrc/gh
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "gh-env-token")
	got = resolveToken("")
	if got != "gh-env-token" {
		t.Errorf("resolveToken() = %q, want %q", got, "gh-env-token")
	}

	// Falls back to netrc, ahead of gh auth token
	t.Setenv("GH_TOKEN", "")
	netrcToken = func() string { return "netrc-token" }
	ghAuthToken = func() string { return "gh-token" }
	got = resolveToken("")
	if got != "netrc-token" {
		t.Errorf("resolveToken() = %q, want %q", got, "netrc-token")
	}

	// Falls back to gh auth token when nothing else is set
	netrcToken = func() string { return "" }
	got = resolveToken("")
	if got != "gh-token" {
		t.Errorf("resolveToken() = %q, want %q", got, "gh-token")
	}

	// Empty when no source provides a token
	ghAuthToken = func() string { return "" }
	got = resolveToken("")
	if got != "" {
		t.Errorf("resolveToken() = %q, want empty", got)
	}
}

func TestNetrcTokenFromFile(t *testing.T) {
	t.Run("returns password for api.github.com", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".netrc")
		content := "machine api.github.com login x password ghp_xxx\n"
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatalf("write netrc: %v", err)
		}
		if got := netrcTokenFromFile(path); got != "ghp_xxx" {
			t.Errorf("netrcTokenFromFile() = %q, want %q", got, "ghp_xxx")
		}
	})

	t.Run("no matching machine returns empty", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".netrc")
		content := "machine example.com login x password secret\n"
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatalf("write netrc: %v", err)
		}
		if got := netrcTokenFromFile(path); got != "" {
			t.Errorf("netrcTokenFromFile() = %q, want empty", got)
		}
	})

	t.Run("nonexistent path returns empty", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "does-not-exist")
		if got := netrcTokenFromFile(path); got != "" {
			t.Errorf("netrcTokenFromFile() = %q, want empty", got)
		}
	})
}

func TestDetectProvider(t *testing.T) {
	tests := []struct {
		arg     string
		want    issueProvider
		wantErr bool
	}{
		{"42", providerGitHub, false},
		{"ENG-123", providerLinear, false},
		{"eng-123", providerLinear, false},
		{"https://linear.app/acme/issue/ENG-123/some-slug", providerLinear, false},
		{"https://linear.app/acme/issue/ENG-123", providerLinear, false},
		{"https://github.com/dokku/docker-port-forward/security/dependabot/20", providerDependabot, false},
		{"https://github.com/dokku/dokku/security/code-scanning/1", providerCodeScanning, false},
		{"not-an-issue", 0, true},
		{"", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			got, err := detectProvider(tt.arg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("detectProvider(%q) error = %v, wantErr %v", tt.arg, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("detectProvider(%q) = %v, want %v", tt.arg, got, tt.want)
			}
		})
	}
}

func TestParseAlertURL(t *testing.T) {
	tests := []struct {
		name       string
		re         *regexp.Regexp
		arg        string
		wantOwner  string
		wantRepo   string
		wantNumber int
		wantOK     bool
	}{
		{
			name:       "dependabot URL",
			re:         dependabotURLRe,
			arg:        "https://github.com/dokku/docker-port-forward/security/dependabot/20",
			wantOwner:  "dokku",
			wantRepo:   "docker-port-forward",
			wantNumber: 20,
			wantOK:     true,
		},
		{
			name:       "code-scanning URL",
			re:         codeScanningURLRe,
			arg:        "https://github.com/dokku/dokku/security/code-scanning/1",
			wantOwner:  "dokku",
			wantRepo:   "dokku",
			wantNumber: 1,
			wantOK:     true,
		},
		{
			name:       "trailing query and fragment tolerated",
			re:         codeScanningURLRe,
			arg:        "https://github.com/owner/repo/security/code-scanning/42?ref=main#x",
			wantOwner:  "owner",
			wantRepo:   "repo",
			wantNumber: 42,
			wantOK:     true,
		},
		{
			name:   "wrong alert type does not match",
			re:     dependabotURLRe,
			arg:    "https://github.com/dokku/dokku/security/code-scanning/1",
			wantOK: false,
		},
		{
			name:   "not a github alert URL",
			re:     dependabotURLRe,
			arg:    "https://linear.app/acme/issue/ENG-123",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, number, ok := parseAlertURL(tt.re, tt.arg)
			if ok != tt.wantOK {
				t.Fatalf("parseAlertURL(%q) ok = %v, want %v", tt.arg, ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if owner != tt.wantOwner || repo != tt.wantRepo || number != tt.wantNumber {
				t.Errorf("parseAlertURL(%q) = (%q, %q, %d), want (%q, %q, %d)", tt.arg, owner, repo, number, tt.wantOwner, tt.wantRepo, tt.wantNumber)
			}
		})
	}
}

func TestParseLinearIdentifier(t *testing.T) {
	tests := []struct {
		arg     string
		want    string
		wantErr bool
	}{
		{"https://linear.app/acme/issue/ENG-123/some-slug", "ENG-123", false},
		{"https://linear.app/acme/issue/eng-123", "ENG-123", false},
		{"ENG-123", "ENG-123", false},
		{"eng-123", "ENG-123", false},
		{"not-an-issue", "", true},
		{"42", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			got, err := parseLinearIdentifier(tt.arg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseLinearIdentifier(%q) error = %v, wantErr %v", tt.arg, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("parseLinearIdentifier(%q) = %q, want %q", tt.arg, got, tt.want)
			}
		})
	}
}

func TestParseLinearURL(t *testing.T) {
	tests := []struct {
		arg            string
		wantIdentifier string
		wantTitle      string
		wantOK         bool
	}{
		{"https://linear.app/acme/issue/ENG-123/fix-the-login-bug", "ENG-123", "fix-the-login-bug", true},
		{"https://linear.app/acme/issue/eng-123/fix", "ENG-123", "fix", true},
		{"https://linear.app/acme/issue/ENG-123", "ENG-123", "", true},
		{"https://linear.app/acme/issue/ENG-123/slug?foo=bar#x", "ENG-123", "slug", true},
		{"ENG-123", "", "", false},
		{"42", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			identifier, title, ok := parseLinearURL(tt.arg)
			if ok != tt.wantOK {
				t.Fatalf("parseLinearURL(%q) ok = %v, want %v", tt.arg, ok, tt.wantOK)
			}
			if identifier != tt.wantIdentifier || title != tt.wantTitle {
				t.Errorf("parseLinearURL(%q) = (%q, %q), want (%q, %q)", tt.arg, identifier, title, tt.wantIdentifier, tt.wantTitle)
			}
		})
	}
}

// TestLinearBranchFromURL covers the offline path: a full URL with a title slug
// is composed into a branch name without any remote lookup.
func TestLinearBranchFromURL(t *testing.T) {
	identifier, title, ok := parseLinearURL("https://linear.app/acme/issue/ENG-123/fix-the-login-bug")
	if !ok || title == "" {
		t.Fatalf("parseLinearURL returned ok=%v title=%q, want a title slug", ok, title)
	}
	if got, want := linearBranchName("jose", identifier, title), "jose/eng-123/fix-the-login-bug"; got != want {
		t.Errorf("linearBranchName = %q, want %q", got, want)
	}
}

func TestLinearBranchName(t *testing.T) {
	tests := []struct {
		name       string
		username   string
		identifier string
		title      string
		want       string
	}{
		{"with username", "jose", "ENG-123", "feat: Add login page", "jose/eng-123/add-login-page"},
		{"empty username omits segment", "", "ENG-123", "feat: Add login page", "eng-123/add-login-page"},
		{"username sanitized", "Jose Diaz", "ABC-7", "Fix bug", "jose-diaz/abc-7/fix-bug"},
		{"lowercase identifier input", "jose", "eng-9", "Simple title", "jose/eng-9/simple-title"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := linearBranchName(tt.username, tt.identifier, tt.title)
			if got != tt.want {
				t.Errorf("linearBranchName(%q, %q, %q) = %q, want %q", tt.username, tt.identifier, tt.title, got, tt.want)
			}
			if len(got) > 240 {
				t.Errorf("branch name exceeds 240 chars: len=%d", len(got))
			}
			if strings.Contains(got, "--") {
				t.Errorf("branch name contains consecutive dashes: %q", got)
			}
			if strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") || strings.HasSuffix(got, "/") {
				t.Errorf("branch name has leading/trailing dash or trailing slash: %q", got)
			}
		})
	}
}

func TestFetchLinearIssue(t *testing.T) {
	var gotAuth, gotContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"issue":{"identifier":"ENG-123","title":"feat: do thing"}}}`)
	}))
	defer server.Close()

	issue, err := fetchLinearIssue(server.URL, "ENG-123", "lin_api_test")
	if err != nil {
		t.Fatalf("fetchLinearIssue() error = %v", err)
	}
	if issue.Identifier != "ENG-123" || issue.Title != "feat: do thing" {
		t.Errorf("fetchLinearIssue() = %+v, want identifier ENG-123 title 'feat: do thing'", issue)
	}
	if gotAuth != "lin_api_test" {
		t.Errorf("Authorization header = %q, want raw token without Bearer", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type header = %q, want application/json", gotContentType)
	}
}

func TestFetchLinearIssueErrors(t *testing.T) {
	t.Run("errors array with HTTP 200", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"errors":[{"message":"Entity not found"}]}`)
		}))
		defer server.Close()

		_, err := fetchLinearIssue(server.URL, "ENG-999", "lin_api_test")
		if err == nil || !strings.Contains(err.Error(), "Entity not found") {
			t.Errorf("fetchLinearIssue() error = %v, want containing 'Entity not found'", err)
		}
	})

	t.Run("null issue", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":{"issue":null}}`)
		}))
		defer server.Close()

		_, err := fetchLinearIssue(server.URL, "ENG-999", "lin_api_test")
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("fetchLinearIssue() error = %v, want containing 'not found'", err)
		}
	})

	t.Run("non-200 status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}))
		defer server.Close()

		_, err := fetchLinearIssue(server.URL, "ENG-1", "bad-token")
		if err == nil || !strings.Contains(err.Error(), "401") {
			t.Errorf("fetchLinearIssue() error = %v, want containing '401'", err)
		}
	})
}

func TestResolveLinearToken(t *testing.T) {
	// Stub the file-based and CLI sources so precedence is tested in isolation.
	origPlain, origTOML, origJSON, origCLI := linearPlainFileToken, linearTOMLToken, linearJSONToken, linearCLIToken
	defer func() {
		linearPlainFileToken, linearTOMLToken, linearJSONToken, linearCLIToken = origPlain, origTOML, origJSON, origCLI
	}()
	linearPlainFileToken = func() string { return "" }
	linearTOMLToken = func() string { return "" }
	linearJSONToken = func() string { return "" }
	linearCLIToken = func() string { return "" }

	t.Setenv("LINEAR_API_KEY", "")

	// Flag takes priority.
	if got := resolveLinearToken("flag-token"); got != "flag-token" {
		t.Errorf("resolveLinearToken() = %q, want %q", got, "flag-token")
	}

	// Falls back to LINEAR_API_KEY.
	t.Setenv("LINEAR_API_KEY", "env-token")
	if got := resolveLinearToken(""); got != "env-token" {
		t.Errorf("resolveLinearToken() = %q, want %q", got, "env-token")
	}

	// Falls back to plain token file, ahead of toml/json.
	t.Setenv("LINEAR_API_KEY", "")
	linearPlainFileToken = func() string { return "plain-token" }
	linearTOMLToken = func() string { return "toml-token" }
	linearJSONToken = func() string { return "json-token" }
	if got := resolveLinearToken(""); got != "plain-token" {
		t.Errorf("resolveLinearToken() = %q, want %q", got, "plain-token")
	}

	// Falls back to toml ahead of json.
	linearPlainFileToken = func() string { return "" }
	if got := resolveLinearToken(""); got != "toml-token" {
		t.Errorf("resolveLinearToken() = %q, want %q", got, "toml-token")
	}

	// Falls back to json ahead of the CLI.
	linearTOMLToken = func() string { return "" }
	linearCLIToken = func() string { return "cli-token" }
	if got := resolveLinearToken(""); got != "json-token" {
		t.Errorf("resolveLinearToken() = %q, want %q", got, "json-token")
	}

	// Falls back to the linear CLI when nothing else is set.
	linearJSONToken = func() string { return "" }
	if got := resolveLinearToken(""); got != "cli-token" {
		t.Errorf("resolveLinearToken() = %q, want %q", got, "cli-token")
	}

	// Empty when no source provides a token.
	linearCLIToken = func() string { return "" }
	if got := resolveLinearToken(""); got != "" {
		t.Errorf("resolveLinearToken() = %q, want empty", got)
	}
}

func TestLinearTokenFromPlainFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("lin_api_plain\n"), 0600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	if got := linearTokenFromPlainFile(path); got != "lin_api_plain" {
		t.Errorf("linearTokenFromPlainFile() = %q, want %q", got, "lin_api_plain")
	}
	if got := linearTokenFromPlainFile(filepath.Join(dir, "missing")); got != "" {
		t.Errorf("linearTokenFromPlainFile(missing) = %q, want empty", got)
	}
}

func TestLinearTokenFromJSON(t *testing.T) {
	dir := t.TempDir()

	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"token":"lin_api_json"}`), 0600); err != nil {
		t.Fatalf("write json: %v", err)
	}
	if got := linearTokenFromJSON(path); got != "lin_api_json" {
		t.Errorf("linearTokenFromJSON() = %q, want %q", got, "lin_api_json")
	}

	altPath := filepath.Join(dir, "alt.json")
	if err := os.WriteFile(altPath, []byte(`{"api_key":"lin_api_alt"}`), 0600); err != nil {
		t.Fatalf("write json: %v", err)
	}
	if got := linearTokenFromJSON(altPath); got != "lin_api_alt" {
		t.Errorf("linearTokenFromJSON(api_key) = %q, want %q", got, "lin_api_alt")
	}

	noKeyPath := filepath.Join(dir, "nokey.json")
	if err := os.WriteFile(noKeyPath, []byte(`{"workspace":"acme"}`), 0600); err != nil {
		t.Fatalf("write json: %v", err)
	}
	if got := linearTokenFromJSON(noKeyPath); got != "" {
		t.Errorf("linearTokenFromJSON(no key) = %q, want empty", got)
	}

	if got := linearTokenFromJSON(filepath.Join(dir, "missing.json")); got != "" {
		t.Errorf("linearTokenFromJSON(missing) = %q, want empty", got)
	}
}

func TestLinearTokenFromTOML(t *testing.T) {
	dir := t.TempDir()

	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("api_key = \"lin_api_toml\"\n"), 0600); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	if got := linearTokenFromTOML(path); got != "lin_api_toml" {
		t.Errorf("linearTokenFromTOML() = %q, want %q", got, "lin_api_toml")
	}

	noKeyPath := filepath.Join(dir, "nokey.toml")
	if err := os.WriteFile(noKeyPath, []byte("default = \"acme\"\n"), 0600); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	if got := linearTokenFromTOML(noKeyPath); got != "" {
		t.Errorf("linearTokenFromTOML(no key) = %q, want empty", got)
	}

	if got := linearTokenFromTOML(filepath.Join(dir, "missing.toml")); got != "" {
		t.Errorf("linearTokenFromTOML(missing) = %q, want empty", got)
	}
}
