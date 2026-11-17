package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Plugins can be installed from a GitHub repository: from one of its
// releases, which installs the release's plugin package (or, when it has
// none, the repository as it was tagged), or from a branch, which installs
// the repository at the branch's latest commit. A plugin is plain
// JavaScript, so the repository itself is a package: plugin.json at its
// root, or in a plugin folder.

// Repo is a GitHub repository.
type Repo struct{ Owner, Name string }

func (r Repo) String() string { return r.Owner + "/" + r.Name }

// URL is the repository's page.
func (r Repo) URL() string { return "https://github.com/" + r.String() }

var repoPart = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)

// ParseRepo reads "owner/repo" or a github.com link to a repository.
func ParseRepo(s string) (Repo, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "git@github.com:")
	if u, err := url.Parse(s); err == nil && (u.Host == "github.com" || u.Host == "www.github.com") {
		s = u.Path
	} else if rest, ok := strings.CutPrefix(s, "github.com/"); ok {
		s = rest
	}
	parts := strings.Split(strings.Trim(s, "/"), "/")
	if len(parts) < 2 {
		return Repo{}, errors.New("give a repository as owner/repo or a github.com link")
	}
	r := Repo{Owner: parts[0], Name: strings.TrimSuffix(parts[1], ".git")}
	if !repoPart.MatchString(r.Owner) || !repoPart.MatchString(r.Name) {
		return Repo{}, fmt.Errorf("%q isn’t a GitHub repository", s)
	}
	return r, nil
}

// Release is one of a repository's releases.
type Release struct {
	Tag, Name  string
	Notes      string // markdown
	Published  time.Time
	Prerelease bool
	// Archive downloads the plugin: the release's package asset, or the
	// repository as tagged.
	Archive string
	// Asset is the package asset's file name; "" when Archive is the
	// tagged repository.
	Asset string
	Size  int64
}

// Branch is one of a repository's branches.
type Branch struct {
	Name, Commit string
}

// Commit is a branch's latest commit.
type Commit struct {
	SHA     string
	Message string
	Date    time.Time
	// Archive downloads the repository at the commit.
	Archive string
}

// Short is the commit's short id, as git shows it.
func (c Commit) Short() string { return ShortSHA(c.SHA) }

// ShortSHA is a commit id cut to 7 characters.
func ShortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// GitHub reads repositories through GitHub's API.
type GitHub struct {
	// Client makes the requests; a client with a timeout when nil.
	Client *http.Client
	// API is the API's root, https://api.github.com when empty.
	API string
}

func (g *GitHub) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (g *GitHub) api() string {
	if g.API != "" {
		return strings.TrimSuffix(g.API, "/")
	}
	return "https://api.github.com"
}

// ErrNoRepo is what a missing or private repository fails with.
var ErrNoRepo = errors.New("no such public repository on GitHub")

func (g *GitHub) getJSON(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.api()+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", UserAgent)
	resp, err := g.client().Do(req)
	if err != nil {
		return fmt.Errorf("couldn’t reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 404:
		return ErrNoRepo
	case resp.StatusCode == 403 || resp.StatusCode == 429:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return errors.New("GitHub limits how often it is asked; try again in an hour")
		}
		return fmt.Errorf("GitHub refused: %s", resp.Status)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return fmt.Errorf("GitHub answered %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v)
}

// DefaultBranch is the repository's default branch.
func (g *GitHub) DefaultBranch(ctx context.Context, r Repo) (string, error) {
	var v struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := g.getJSON(ctx, "/repos/"+r.String(), &v); err != nil {
		return "", err
	}
	return v.DefaultBranch, nil
}

// Releases are the repository's published releases, newest first.
func (g *GitHub) Releases(ctx context.Context, r Repo) ([]Release, error) {
	var vs []struct {
		TagName     string    `json:"tag_name"`
		Name        string    `json:"name"`
		Body        string    `json:"body"`
		Draft       bool      `json:"draft"`
		Prerelease  bool      `json:"prerelease"`
		PublishedAt time.Time `json:"published_at"`
		ZipballURL  string    `json:"zipball_url"`
		Assets      []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := g.getJSON(ctx, "/repos/"+r.String()+"/releases?per_page=20", &vs); err != nil {
		return nil, err
	}
	var out []Release
	for _, v := range vs {
		if v.Draft {
			continue
		}
		rel := Release{Tag: v.TagName, Name: v.Name, Notes: v.Body, Published: v.PublishedAt, Prerelease: v.Prerelease, Archive: v.ZipballURL}
		for _, a := range v.Assets {
			if strings.HasSuffix(a.Name, PackageExt) || rel.Asset == "" && strings.HasSuffix(a.Name, ".zip") {
				rel.Archive, rel.Asset, rel.Size = a.URL, a.Name, a.Size
			}
		}
		out = append(out, rel)
	}
	return out, nil
}

// Latest is the newest release that isn't a pre-release; false when
// there is none.
func Latest(rels []Release) (Release, bool) {
	for _, r := range rels {
		if !r.Prerelease {
			return r, true
		}
	}
	return Release{}, false
}

// Branches are the repository's branches.
func (g *GitHub) Branches(ctx context.Context, r Repo) ([]Branch, error) {
	var vs []struct {
		Name   string `json:"name"`
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := g.getJSON(ctx, "/repos/"+r.String()+"/branches?per_page=50", &vs); err != nil {
		return nil, err
	}
	out := make([]Branch, 0, len(vs))
	for _, v := range vs {
		out = append(out, Branch{Name: v.Name, Commit: v.Commit.SHA})
	}
	return out, nil
}

// Commit is branch's latest commit.
func (g *GitHub) Commit(ctx context.Context, r Repo, branch string) (Commit, error) {
	var v struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message   string `json:"message"`
			Committer struct {
				Date time.Time `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	if err := g.getJSON(ctx, "/repos/"+r.String()+"/commits/"+url.PathEscape(branch), &v); err != nil {
		if errors.Is(err, ErrNoRepo) {
			return Commit{}, fmt.Errorf("%s has no branch %q", r, branch)
		}
		return Commit{}, err
	}
	msg, _, _ := strings.Cut(v.Commit.Message, "\n")
	return Commit{SHA: v.SHA, Message: msg, Date: v.Commit.Committer.Date, Archive: g.api() + "/repos/" + r.String() + "/zipball/" + v.SHA}, nil
}

// Download saves the archive at rawURL, a release asset or a zipball, in
// dir and returns its path.
func (g *GitHub) Download(ctx context.Context, rawURL, dir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := g.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("couldn’t download the plugin: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("couldn’t download the plugin: GitHub answered %s", resp.Status)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "plugin-*.zip")
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, MaxPackageSize+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > MaxPackageSize {
		err = fmt.Errorf("the download is over %d MB", MaxPackageSize>>20)
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return filepath.Clean(f.Name()), nil
}

// Readme is the repository's README, as Markdown, at ref (a tag, a branch
// or a commit); "" when it has none.
func (g *GitHub) Readme(ctx context.Context, r Repo, ref string) (string, error) {
	u := g.api() + "/repos/" + r.String() + "/readme"
	if ref != "" {
		u += "?ref=" + url.QueryEscape(ref)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github.raw")
	req.Header.Set("User-Agent", UserAgent)
	resp, err := g.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("couldn’t reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 404:
		return "", nil
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return "", fmt.Errorf("GitHub answered %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return string(data), err
}
