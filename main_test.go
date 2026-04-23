package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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
	// Flag takes priority
	got := resolveToken("flag-token")
	if got != "flag-token" {
		t.Errorf("resolveToken() = %q, want %q", got, "flag-token")
	}

	// Falls back to env var
	t.Setenv("GITHUB_TOKEN", "env-token")
	got = resolveToken("")
	if got != "env-token" {
		t.Errorf("resolveToken() = %q, want %q", got, "env-token")
	}

	// Empty when neither set
	t.Setenv("GITHUB_TOKEN", "")
	got = resolveToken("")
	if got != "" {
		t.Errorf("resolveToken() = %q, want empty", got)
	}
}
