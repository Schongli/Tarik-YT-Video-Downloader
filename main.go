//go:build windows

package main

import (
	"archive/zip"
	"bufio"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	webview "github.com/jchv/go-webview2"
)

//go:embed ui/index.html
var page string

//go:embed ui/fonts/archivo.woff2
var fontDisplay []byte

//go:embed ui/fonts/geist-mono.woff2
var fontMono []byte

//go:embed engine/yt-dlp.exe
var bundledEngine []byte

const denoURL = "https://github.com/denoland/deno/releases/latest/download/deno-x86_64-pc-windows-msvc.zip"

var (
	w       webview.WebView
	home    = filepath.Join(os.Getenv("LOCALAPPDATA"), "Tarik")
	engine  = filepath.Join(home, "yt-dlp.exe")
	running = map[string]*exec.Cmd{}
	mu      sync.Mutex

	denoOnce  sync.Once
	denoState = "checking" // checking | ready | downloading | error
	denoErr   string
)

// emit pushes an event to the page; JS receives it in window.onGo(type, data).
func emit(kind string, data any) {
	b, _ := json.Marshal(data)
	w.Dispatch(func() { w.Eval(fmt.Sprintf("window.onGo(%q,%s)", kind, b)) })
}

func hidden(c *exec.Cmd) *exec.Cmd {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return c
}

// command runs the engine without flashing a console window.
func command(args ...string) *exec.Cmd {
	c := hidden(exec.Command(engine, engineArgs(args...)...))
	c.Dir = home // not wherever Tarik.exe happens to sit, e.g. a Downloads folder full of strangers' files
	return c
}

func ensureEngine() error {
	os.Remove(filepath.Join(home, "youtube-dl.exe")) // engine of the first Tarik build
	if _, err := os.Stat(engine); err == nil {
		return nil
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	return os.WriteFile(engine, bundledEngine, 0o755)
}

// findTool looks next to the app, in the app data folder, then on PATH.
func findTool(name string) string {
	exe, _ := os.Executable()
	for _, p := range []string{filepath.Join(filepath.Dir(exe), name+".exe"), filepath.Join(home, name+".exe")} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

func ffmpegPath() string { return findTool("ffmpeg") }
func denoPath() string   { return findTool("deno") }

func engineStatus() {
	out, err := command("--version").Output()
	mu.Lock()
	ds, de := denoState, denoErr
	mu.Unlock()
	emit("engine", map[string]any{"version": strings.TrimSpace(string(out)), "ok": err == nil, "path": engine,
		"error": errString(err), "ffmpeg": ffmpegPath(), "deno": denoPath(), "denoState": ds, "denoError": de})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func setDeno(state, e string) {
	mu.Lock()
	denoState, denoErr = state, e
	mu.Unlock()
	engineStatus()
}

// ensureDeno fetches Deno once if it is missing: yt-dlp needs a JS runtime for YouTube.
func ensureDeno() {
	if denoPath() != "" {
		setDeno("ready", "")
		return
	}
	setDeno("downloading", "")
	if err := fetchDeno(); err != nil {
		setDeno("error", err.Error())
		return
	}
	setDeno("ready", "")
}

type counter struct {
	n, total int64
	last     time.Time
}

func (c *counter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	if time.Since(c.last) > 250*time.Millisecond && c.total > 0 {
		c.last = time.Now()
		emit("deno", map[string]any{"percent": float64(c.n) * 100 / float64(c.total), "size": bytes(float64(c.total))})
	}
	return len(p), nil
}

func fetchDeno() error {
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Get(denoURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("GitHub answered " + resp.Status)
	}
	zipPath := filepath.Join(home, "deno.zip.part")
	defer os.Remove(zipPath)
	f, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	_, err = io.Copy(io.MultiWriter(f, &counter{total: resp.ContentLength}), resp.Body)
	f.Close()
	if err != nil {
		return err
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		if zf.Name != "deno.exe" {
			continue
		}
		src, err := zf.Open()
		if err != nil {
			return err
		}
		defer src.Close()
		tmp := filepath.Join(home, "deno.exe.new")
		dst, err := os.Create(tmp)
		if err != nil {
			return err
		}
		n, err := io.Copy(dst, src)
		dst.Close()
		if err != nil || n < 10<<20 { // the real binary is ~100 MB
			os.Remove(tmp)
			return errors.New("Deno download was incomplete")
		}
		return os.Rename(tmp, filepath.Join(home, "deno.exe"))
	}
	return errors.New("deno.exe not found in the download")
}

// updateEngine uses yt-dlp's own updater, which swaps the exe in place.
func updateEngine() {
	out, err := command("-U").CombinedOutput()
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	msg := strings.TrimSpace(lines[len(lines)-1])
	if err != nil && msg == "" {
		msg = err.Error()
	}
	emit("toast", strings.TrimPrefix(msg, "ERROR: "))
	engineStatus()
}

func probe(id, url, cookies string) {
	ffmpeg := ffmpegPath()
	out, err := command(probeArgs(url, ffmpeg, denoPath(), cookies)...).Output()
	if err != nil {
		msg := err.Error()
		if ee, ok := err.(*exec.ExitError); ok {
			msg = lastError(string(ee.Stderr))
		}
		emit("info", map[string]any{"id": id, "error": msg})
		return
	}
	in, err := summarize(out, ffmpeg != "")
	emit("info", map[string]any{"id": id, "info": in, "error": errString(err)})
}

// lastError picks the readable "ERROR:" line out of yt-dlp's stderr.
func lastError(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); strings.HasPrefix(l, "ERROR: ") {
			return strings.TrimPrefix(l, "ERROR: ")
		}
	}
	if len(lines) > 0 && lines[len(lines)-1] != "" {
		return lines[len(lines)-1]
	}
	return "yt-dlp could not read this link."
}

func download(id string, o Options) {
	c := command(buildArgs(o, ffmpegPath(), denoPath())...)
	stdout, _ := c.StdoutPipe()
	c.Stderr = c.Stdout // ERROR lines arrive on stderr; read both in order
	if err := c.Start(); err != nil {
		emit("progress", map[string]any{"id": id, "p": Progress{Percent: -1, Error: err.Error()}, "done": true})
		return
	}
	mu.Lock()
	running[id] = c
	mu.Unlock()

	file, failure, saved := "", "", 0
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	last := time.Time{}
	for sc.Scan() {
		p, ok := parseLine(sc.Text())
		if !ok {
			continue
		}
		if p.File != "" {
			file = p.File
			saved++
		}
		if p.Error != "" {
			failure = p.Error
		}
		// Progress lines can arrive dozens of times a second; ~10 updates/s is plenty.
		if p.Size+p.Got != "" && p.Percent < 100 && time.Since(last) < 100*time.Millisecond {
			continue
		}
		last = time.Now()
		emit("progress", map[string]any{"id": id, "p": p})
	}
	err := c.Wait()

	mu.Lock()
	_, stillOurs := running[id]
	delete(running, id)
	mu.Unlock()
	switch {
	case !stillOurs:
		emit("progress", map[string]any{"id": id, "done": true, "cancelled": true})
	case err != nil:
		if failure == "" {
			failure = err.Error()
		}
		// A playlist can fail on some videos and still save the rest.
		emit("progress", map[string]any{"id": id, "done": true, "file": file, "saved": saved, "p": Progress{Percent: -1, Error: failure}})
	default:
		var size int64
		if st, err := os.Stat(file); err == nil && saved == 1 {
			size = st.Size()
		}
		emit("progress", map[string]any{"id": id, "done": true, "file": file, "saved": saved, "bytes": size})
	}
	if stillOurs {
		flashIfBackground(uintptr(w.Window()))
	}
}

func cancel(id string) {
	mu.Lock()
	c := running[id]
	delete(running, id)
	mu.Unlock()
	if c != nil && c.Process != nil {
		// /T also stops the ffmpeg child that yt-dlp may have started.
		hidden(exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(c.Process.Pid))).Run()
	}
}

func extractors() {
	out, err := command("--list-extractors", "--encoding", "utf-8").Output()
	var list []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.Contains(l, "(CURRENTLY BROKEN)") {
			list = append(list, l)
		}
	}
	emit("extractors", map[string]any{"list": list, "error": errString(err)})
}

func settingsFile() string { return filepath.Join(home, "settings.json") }
func historyFile() string  { return filepath.Join(home, "history.json") }

func defaultDir() string {
	// shortcut: assumes the Downloads folder was not relocated; SHGetKnownFolderPath if someone asks.
	return filepath.Join(os.Getenv("USERPROFILE"), "Downloads")
}

func main() {
	if err := ensureEngine(); err != nil {
		messageBox("Tarik", "Could not set up yt-dlp in "+home+":\n"+err.Error())
		return
	}
	w = webview.NewWithOptions(webview.WebViewOptions{
		DataPath:      filepath.Join(home, "webview"),
		WindowOptions: webview.WindowOptions{Title: "Tarik", Width: 1000, Height: 820, IconId: 2, Center: true},
	})
	if w == nil {
		messageBox("Tarik", "Microsoft Edge WebView2 Runtime is missing.\nGet it from https://go.microsoft.com/fwlink/p/?LinkId=2124703")
		return
	}
	defer w.Destroy()
	w.SetSize(760, 560, webview.HintMin)
	darkTitleBar(uintptr(w.Window()))

	w.Bind("goBoot", func() map[string]any {
		s, _ := os.ReadFile(settingsFile())
		h, _ := os.ReadFile(historyFile())
		go engineStatus()
		go denoOnce.Do(ensureDeno)
		return map[string]any{"settings": string(s), "history": string(h), "defaultDir": defaultDir()}
	})
	w.Bind("goSave", func(s string) { os.WriteFile(settingsFile(), []byte(s), 0o644) })
	w.Bind("goSaveHistory", func(s string) { os.WriteFile(historyFile(), []byte(s), 0o644) })
	// The page picks the ids, so no result can arrive before the page knows what it is waiting for.
	w.Bind("goProbe", func(id, url, cookies string) { go probe(id, url, cookies) })
	w.Bind("goDownload", func(id string, o Options) {
		if o.Dir == "" {
			o.Dir = defaultDir()
		}
		go download(id, o)
	})
	w.Bind("goCancel", cancel)
	w.Bind("goTitleBar", func(dark bool) { setTitleBar(uintptr(w.Window()), dark) })
	w.Bind("goClipboard", clipboardText)
	w.Bind("goUpdate", func() { go updateEngine() })
	w.Bind("goRetryDeno", func() { go ensureDeno() })
	w.Bind("goExtractors", func() { go extractors() })
	w.Bind("goPickFolder", func(start string) {
		go func() {
			if dir := pickFolder(uintptr(w.Window()), start); dir != "" {
				emit("folder", dir)
			}
		}()
	})
	w.Bind("goReveal", func(path string) {
		if _, err := os.Stat(path); err == nil {
			exec.Command("explorer", "/select,", path).Start()
		} else {
			shellOpen(filepath.Dir(path))
		}
	})
	w.Bind("goOpen", func(target string) bool {
		if strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "http://") {
			shellOpen(target)
			return true
		}
		if _, err := os.Stat(target); err != nil || !filepath.IsAbs(target) {
			return false // a download that was moved or deleted since
		}
		if playable(target) {
			shellOpen(target)
		} else { // never "open" something that could run; show it instead
			exec.Command("explorer", "/select,", target).Start()
		}
		return true
	})

	nonce := make([]byte, 16)
	rand.Read(nonce)
	w.SetHtml(strings.NewReplacer(
		"{{NONCE}}", base64.StdEncoding.EncodeToString(nonce), // only our own script may run, see the CSP in index.html
		"{{FONT_DISPLAY}}", base64.StdEncoding.EncodeToString(fontDisplay),
		"{{FONT_MONO}}", base64.StdEncoding.EncodeToString(fontMono)).Replace(page))
	w.Run()

	mu.Lock()
	ids := make([]string, 0, len(running))
	for id := range running {
		ids = append(ids, id)
	}
	mu.Unlock()
	for _, id := range ids {
		cancel(id)
	}
}
