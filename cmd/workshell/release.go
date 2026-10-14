package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// runRelease implements `workshell release`: it reads git metadata, writes a
// WorkshellRelease.json module (Rojo syncs JSON files as ModuleScripts, and
// the SDK picks it up automatically), generates a Rojo sourcemap, and
// registers everything with the Workshell server.
func runRelease(args []string) error {
	fs := flag.NewFlagSet("release", flag.ContinueOnError)
	url := fs.String("url", os.Getenv("WORKSHELL_URL"), "Workshell server URL (or $WORKSHELL_URL)")
	key := fs.String("key", os.Getenv("WORKSHELL_KEY"), "project ingest key (or $WORKSHELL_KEY)")
	signing := fs.String("signing-secret", os.Getenv("WORKSHELL_SIGNING_SECRET"), "request signing secret, if the project requires signatures")
	ver := fs.String("version", "", "release name (default: short git commit, with -dirty for uncommitted changes)")
	project := fs.String("project", "default.project.json", "Rojo project file used to generate the sourcemap")
	sourcemap := fs.String("sourcemap", "", "use an existing `rojo sourcemap` JSON file instead of running rojo")
	out := fs.String("out", "", "write a WorkshellRelease.json module here (e.g. src/shared/WorkshellRelease.json)")
	placeVersion := fs.String("place-version", "", "Roblox place version this release was published as")
	dryRun := fs.Bool("dry-run", false, "print what would be sent instead of uploading")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: workshell release [flags]\n\nTypical CI step, before `rojo build`:\n  workshell release -out src/shared/WorkshellRelease.json\n\nFlags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	commit := git("rev-parse", "HEAD")
	branch := git("rev-parse", "--abbrev-ref", "HEAD")
	message := git("log", "-1", "--pretty=%s")
	author := git("log", "-1", "--pretty=%an")
	if *ver == "" {
		if commit == "" {
			return errors.New("not a git repository; pass -version")
		}
		*ver = commit[:min(12, len(commit))]
		if git("status", "--porcelain", "--untracked-files=no") != "" {
			*ver += "-dirty"
		}
	}

	if *out != "" {
		module, _ := json.MarshalIndent(map[string]string{
			"version": *ver, "commit": commit, "branch": branch, "builtAt": time.Now().UTC().Format(time.RFC3339),
		}, "", "  ")
		if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(*out, append(module, '\n'), 0o644); err != nil {
			return err
		}
		fmt.Printf("wrote %s\n", *out)
	}

	var smap json.RawMessage
	switch {
	case *sourcemap != "":
		b, err := os.ReadFile(*sourcemap)
		if err != nil {
			return err
		}
		if !json.Valid(b) {
			return fmt.Errorf("%s is not a valid sourcemap; create one with `rojo sourcemap`", *sourcemap)
		}
		smap = b
	default:
		if _, err := os.Stat(*project); err == nil {
			if _, err := exec.LookPath("rojo"); err == nil {
				b, err := exec.Command("rojo", "sourcemap", *project).Output()
				if err != nil {
					return fmt.Errorf("rojo sourcemap: %w", err)
				}
				smap = b
			} else {
				fmt.Println("rojo not found; uploading without a sourcemap (stack frames won't link to files)")
			}
		}
	}

	if len(smap) > 0 && !json.Valid(smap) {
		return errors.New("rojo sourcemap printed invalid JSON")
	}
	body, err := json.Marshal(map[string]any{
		"version": *ver, "commit": commit, "branch": branch, "message": message, "author": author,
		"placeVersion": *placeVersion, "sourcemap": smap,
	})
	if err != nil {
		return err
	}
	if *dryRun {
		var pretty bytes.Buffer
		json.Indent(&pretty, body, "", "  ")
		if pretty.Len() > 4000 {
			fmt.Println(pretty.String()[:4000] + "\n…")
		} else {
			fmt.Println(pretty.String())
		}
		return nil
	}
	if *url == "" || *key == "" {
		return errors.New("-url and -key (or $WORKSHELL_URL and $WORKSHELL_KEY) are required")
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(*url, "/")+"/api/v1/releases", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Workshell-Key", *key)
	if *signing != "" {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		m := hmac.New(sha256.New, []byte(*signing))
		m.Write([]byte(ts + "."))
		m.Write(body)
		req.Header.Set("X-Workshell-Timestamp", ts)
		req.Header.Set("X-Workshell-Signature", "v1="+hex.EncodeToString(m.Sum(nil)))
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var res struct {
		SourcemapScripts int `json:"sourcemapScripts"`
	}
	json.Unmarshal(respBody, &res)
	fmt.Printf("registered release %s (commit %s, %d scripts mapped)\n", *ver, short(commit), res.SourcemapScripts)
	return nil
}

func git(args ...string) string {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func short(s string) string { return s[:min(7, len(s))] }
