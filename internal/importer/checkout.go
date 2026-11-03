package importer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// PrepareCheckout never follows symlinks or evaluates repository hooks. Its
// result is a bounded working-tree observation, not a claim of a clean commit.
func PrepareCheckout(directory, sourceID string) (Batch, error) {
	result := Batch{SourceID: sourceID, Files: map[string]string{}, Assets: map[string][]byte{}, Complete: true, withheldReasons: map[string]int{}}
	withhold := func(reason string) {
		result.Withheld++
		result.Complete = false
		result.withheldReasons[reason]++
	}
	if !domain.ValidID(sourceID) {
		return result, domain.ErrInvalid
	}
	info, e := os.Lstat(directory)
	if e != nil || !info.IsDir() {
		return result, domain.ErrInvalid
	}
	root, e := os.OpenRoot(directory)
	if e != nil {
		return result, e
	}
	defer root.Close()
	read := func(name string, limit int64) ([]byte, error) {
		f, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if e != nil {
			return nil, e
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() {
			return nil, domain.ErrInvalid
		}
		b, e := io.ReadAll(io.LimitReader(f, limit+1))
		if e != nil || int64(len(b)) > limit {
			return nil, domain.ErrInvalid
		}
		return b, nil
	}
	git := func(args ...string) (string, error) {
		argv := append([]string{"-C", directory, "-c", "core.fsmonitor=false"}, args...)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", argv...)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
		var out boundedGitOutput
		cmd.Stdout = &out
		e := cmd.Run()
		value := out.String()
		if len(args) > 0 && args[0] == "ls-files" {
			return value, e
		}
		return strings.TrimSpace(value), e
	}
	top, e := git("rev-parse", "--show-toplevel")
	if e != nil {
		return result, errors.New("selected directory is not a Git checkout")
	}
	selected, e := filepath.EvalSymlinks(directory)
	if e != nil {
		return result, e
	}
	selected, e = filepath.Abs(selected)
	if e != nil {
		return result, e
	}
	top, e = filepath.EvalSymlinks(top)
	if e != nil || selected != top {
		return result, errors.New("select the Git checkout root")
	}
	head, e := git("rev-parse", "--verify", "HEAD")
	if e != nil || !domain.ValidCommitID(head) {
		return result, errors.New("checkout requires an existing Git commit")
	}
	result.Commit = head
	status, e := git("status", "--porcelain", "--untracked-files=no")
	if e != nil {
		return result, e
	}
	result.Dirty = status != ""
	unmergedText, e := git("ls-files", "--unmerged", "-z")
	if e != nil {
		return result, e
	}
	unmerged := map[string]bool{}
	for _, record := range strings.Split(unmergedText, "\x00") {
		if record == "" {
			continue
		}
		_, name, ok := strings.Cut(record, "\t")
		if !ok || name == "" {
			return result, domain.ErrInvalid
		}
		unmerged[name] = true
	}
	if unmerged["veda.yaml"] {
		return result, domain.ErrConflict
	}
	trackedText, e := git("ls-files", "-z", "--cached")
	if e != nil {
		return result, e
	}
	tracked := map[string]bool{}
	directories := map[string]bool{".": true}
	for _, name := range strings.Split(trackedText, "\x00") {
		if name == "" {
			continue
		}
		tracked[name] = true
		if len(tracked) > 20000 {
			//lint:ignore ST1005 "Git" is a proper noun; the message text is observable and must not change
			return result, errors.New("Git index exceeds import limits")
		}
		for dir := path.Dir(name); dir != "." && dir != "/"; dir = path.Dir(dir) {
			directories[dir] = true
		}
	}
	if info, e := fs.Lstat(root.FS(), "veda.yaml"); e == nil {
		if !info.Mode().IsRegular() || !tracked["veda.yaml"] {
			return result, domain.ErrInvalid
		}
		b, e := read("veda.yaml", 16384)
		if e != nil {
			return result, e
		}
		result.Metadata, e = ParseMetadata(b)
		if e != nil {
			return result, e
		}
		result.Kind = "wiki"
	} else if !errors.Is(e, fs.ErrNotExist) {
		return result, e
	} else {
		info, e := fs.Lstat(root.FS(), "docs")
		if errors.Is(e, fs.ErrNotExist) {
			return result, domain.ErrNoDocs
		}
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return result, domain.ErrInvalid
		}
		if !directories["docs"] {
			return result, domain.ErrNoDocs
		}
		result.Kind = "program"
	}
	start := "."
	if result.Kind == "program" {
		start = "docs"
	}
	// Git can leave an unmerged path absent from the working tree. Count it
	// before traversal so a partial import cannot mistake it for a deletion.
	for name := range unmerged {
		if !domain.ImportablePath(name, result.Kind) {
			continue
		}
		switch strings.ToLower(path.Ext(name)) {
		case ".md", ".markdown", ".png", ".jpg", ".jpeg", ".webp", ".gif":
			withhold("unmerged")
		}
	}
	total := 0
	count := 0
	entries := 0
	e = fs.WalkDir(root.FS(), start, func(name string, entry fs.DirEntry, walkErr error) error {
		entries++
		if entries > 20000 {
			return errors.New("checkout traversal limit exceeded")
		}
		if walkErr != nil {
			return walkErr
		}
		if name == "." || name == "docs" {
			return nil
		}
		if entry.IsDir() {
			if !directories[name] {
				return fs.SkipDir
			}
			if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" || entry.Name() == "vendor" || entry.Name() == "dist" {
				return fs.SkipDir
			}
			return nil
		}
		if !tracked[name] || !domain.SafeRelative(name) {
			return nil
		}
		if unmerged[name] {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			withhold("symlink")
			return nil
		}
		if !entry.Type().IsRegular() {
			return domain.ErrInvalid
		}
		ext := strings.ToLower(path.Ext(name))
		markdown := ext == ".md" || ext == ".markdown"
		image := ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" || ext == ".gif"
		if !markdown && !image {
			return nil
		}
		limit := int64(domain.MaxArticleBytes)
		if image {
			limit = 4 << 20
		}
		b, e := read(name, limit)
		if e != nil {
			return e
		}
		count++
		total += len(b)
		if count > 2000 || total > MaxBytes {
			return errors.New("checkout exceeds import limits")
		}
		if markdown {
			if !utf8.Valid(b) {
				withhold("invalid_utf8")
				return nil
			}
			if domain.CredentialLikeBytes(b) {
				withhold("sensitive_pattern")
				return nil
			}
			result.Files[name] = string(b)
		} else {
			if _, e := domain.ImageRef(b); e != nil {
				withhold("unsupported_image")
				return nil
			}
			result.Assets[name] = b
		}
		return nil
	})
	if e != nil {
		return result, e
	}
	if result.Metadata != nil && result.Metadata.Image != "" {
		if _, ok := result.Assets[result.Metadata.Image]; !ok {
			return result, errors.New("metadata image is unavailable or unsafe")
		}
	}
	after, e := git("rev-parse", "--verify", "HEAD")
	if e != nil || after != head {
		return result, domain.ErrConflict
	}
	statusAfter, e := git("status", "--porcelain", "--untracked-files=no")
	if e != nil || statusAfter != status {
		return result, domain.ErrConflict
	}
	unmergedAfter, e := git("ls-files", "--unmerged", "-z")
	if e != nil || unmergedAfter != unmergedText {
		return result, domain.ErrConflict
	}
	return result, nil
}

// Git commands have bounded output even for adversarial repository indexes.
type boundedGitOutput struct{ bytes.Buffer }

// Write implements io.Writer and fails once the output would pass the limit.
func (b *boundedGitOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 4<<20 {
		//lint:ignore ST1005 "Git" is a proper noun; the message text is observable and must not change
		return 0, errors.New("Git output exceeds import limit")
	}
	return b.Buffer.Write(p)
}
