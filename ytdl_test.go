package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseLine(t *testing.T) {
	cases := []struct {
		in   string
		want Progress
	}{
		{"[tarik] downloading|45.3|13000000|NA|1290000.5|10|5889000|avc1.640028",
			Progress{Percent: 45.3, Size: "12.4 MB", Speed: "1.2 MB/s", Bps: 1290000.5, ETA: "0:10", Part: "video"}},
		{"[tarik] downloading|NA|NA|102860000|NA|NA|1024|none", Progress{Percent: -1, Size: "~98.1 MB", Part: "audio"}},
		{"[tarik] downloading|NA|NA|NA|2048|NA|5242880|NA", Progress{Percent: -1, Got: "5.0 MB", Speed: "2 KB/s", Bps: 2048}},
		{"[tarik] downloading|12.5|NA|NA|2048|3725|100|NA", Progress{Percent: 12.5, Got: "100 B", Speed: "2 KB/s", Bps: 2048, ETA: "1:02:05"}},
		{"[tarik] finished|100.0|13000000|NA|5000000|NA|13000000|NA", Progress{Percent: 100, Size: "12.4 MB", Speed: "4.8 MB/s", Bps: 5000000}},
		{"[tarik-pp] Merger|started", Progress{Percent: -1, Stage: "Joining video and audio"}},
		{"[tarik-pp] FixupM3u8|started", Progress{Percent: -1, Stage: "Tidying the file"}},
		{`[tarik-item] {"title": "Lagu \u2013 \u00c4", "thumbnail": "https://i/x.jpg", "playlist_index": 2, "playlist_count": 10}`,
			Progress{Percent: -1, Title: "Lagu – Ä", Thumb: "https://i/x.jpg", Index: 2, Count: 10}},
		{`[tarik-item] {"title": "Solo"}`, Progress{Percent: -1, Title: "Solo"}},
		{`[tarik-file] "C:\\x\\Lagu \u2013 \u00c4.mp4"`, Progress{Percent: -1, File: `C:\x\Lagu – Ä.mp4`}},
		{"ERROR: [youtube] abc: Video unavailable", Progress{Percent: -1, Error: "[youtube] abc: Video unavailable"}},
	}
	for _, c := range cases {
		got, ok := parseLine(c.in)
		if !ok || got != c.want {
			t.Errorf("%q\n got %+v ok=%v\nwant %+v", c.in, got, ok, c.want)
		}
	}
	for _, s := range []string{"WARNING: something", "[tarik] bad", "[tarik-pp] Merger|finished", "[tarik-pp] MoveFiles|started",
		"[tarik-item] not json", `[tarik-file] ""`, "[download] 5% of 1MiB"} {
		if _, ok := parseLine(s); ok {
			t.Errorf("%q should be ignored", s)
		}
	}
}

func has(t *testing.T, args []string, wants ...string) {
	t.Helper()
	a := strings.Join(args, " ") + " "
	for _, w := range wants {
		if !strings.Contains(a, w) {
			t.Errorf("missing %q in %s", w, a)
		}
	}
}

func lacks(t *testing.T, args []string, bad ...string) {
	t.Helper()
	a := strings.Join(args, " ")
	for _, b := range bad {
		if strings.Contains(a, b) {
			t.Errorf("unexpected %q in %s", b, a)
		}
	}
}

func TestBuildArgs(t *testing.T) {
	ff := "C:/ff.exe"
	// No ffmpeg: one ready-made file, and nothing that needs converting.
	a := buildArgs(Options{URL: "u", Dir: "D", Res: 720, Subs: []string{"en"}, End: 9, Sponsor: true, Embed: true}, "", "")
	has(t, a, "-S res:720,vcodec:h264,acodec:aac", "-f b ", "--no-playlist", "--quiet --progress --newline --no-simulate", "--sub-langs en")
	lacks(t, a, "--js-runtimes", "--download-sections", "--sponsorblock", "--embed", "--merge-output-format")
	if a[len(a)-2] != "--" || a[len(a)-1] != "u" {
		t.Error("URL must come last, after --")
	}

	a = buildArgs(Options{URL: "u", Dir: "D", Subs: []string{"en", "id"}, Sponsor: true, Embed: true, Name: "channel", Rate: "2M", Cookies: "firefox"}, ff, "C:/deno.exe")
	has(t, a, "-S res,vcodec", "-f bv*+ba/b --merge-output-format mp4", "--js-runtimes deno:C:/deno.exe", "--ffmpeg-location C:/ff.exe",
		"--sub-langs en,id --embed-subs", "--sponsorblock-remove sponsor", "--embed-metadata --embed-thumbnail",
		"%(uploader,channel|Unknown).60B - %(title).150B.%(ext)s", "--limit-rate 2M", "--cookies-from-browser firefox")

	// Trimming names the clip and skips sponsor cutting (the two fight over the timeline).
	a = buildArgs(Options{URL: "u", Dir: "D", Start: 12.5, End: 3725, Sponsor: true}, ff, "")
	has(t, a, "--download-sections *12.50-3725.00 --force-keyframes-at-cuts", "%(title).150B [0m12s-1h02m05s].%(ext)s")
	lacks(t, a, "--sponsorblock")

	a = buildArgs(Options{URL: "u", Dir: "D", Kind: "audio", Audio: "wav", Embed: true, Subs: []string{"en"}, Playlist: true, Items: "1-3,7", End: 5}, ff, "")
	has(t, a, "-x --audio-format wav", "--yes-playlist --playlist-items 1-3,7", "%(playlist_index)s - %(title).150B.%(ext)s", "--embed-metadata")
	lacks(t, a, "--embed-thumbnail", "--sub-langs", "-S ", "--download-sections")
	has(t, buildArgs(Options{URL: "u", Kind: "audio", Audio: "m4a"}, ff, ""), "-f ba[ext=m4a]/ba/b -x --audio-format m4a")
	has(t, buildArgs(Options{URL: "u", Kind: "audio", Audio: "opus"}, "", ""), "-f ba[ext=m4a]/ba/b --")

	has(t, probeArgs("u", "", "d", "edge"), "-J --flat-playlist", "deno:d", "--cookies-from-browser edge")
}

// Nothing the page sends may turn into an extra yt-dlp option.
func TestClean(t *testing.T) {
	o := Options{Kind: "x", Audio: "--exec", Name: "../x", Res: -5, Subs: []string{"en", "--exec=calc", "", "pt-BR"},
		Items: "1;rm", Rate: "--proxy", Cookies: "--config-locations", Start: 9, End: 3}.clean()
	want := Options{Kind: "video", Audio: "mp3", Name: "title", Subs: []string{"en", "pt-BR"}}
	if !reflect.DeepEqual(o, want) {
		t.Errorf("got %+v\nwant %+v", o, want)
	}
	a := buildArgs(Options{URL: "--exec=calc", Cookies: "--x", Rate: "9", Items: "--y"}, "", "")
	lacks(t, a[:len(a)-2], "--exec", "--x", "--y", "--limit-rate", "--cookies")
}

func TestPlayable(t *testing.T) {
	for path, want := range map[string]bool{`C:\x\a.mp4`: true, `C:\x\a.MP3`: true, `C:\x\a.webm`: true,
		`C:\x\a.exe`: false, `C:\x\a.mp4.bat`: false, `C:\x\a.lnk`: false, `C:\x\a`: false, `C:\x\a.html`: false} {
		if playable(path) != want {
			t.Errorf("playable(%q) = %v", path, !want)
		}
	}
	if a := engineArgs("-U"); a[0] != "--ignore-config" || a[1] != "-U" {
		t.Error(a)
	}
}

func TestSummarize(t *testing.T) {
	raw := `{"_type":"video","title":"T","uploader":"U","extractor_key":"Youtube","upload_date":"20240131",
	"duration":100,"view_count":1234,"like_count":null,"webpage_url":"https://w","thumbnail":"https://i",
	"formats":[
	 {"format_id":"sb0","height":90,"width":160,"vcodec":"none","acodec":"none"},
	 {"format_id":"140","vcodec":"none","acodec":"mp4a.40.2","ext":"m4a","filesize":1000},
	 {"format_id":"251","vcodec":"none","acodec":"opus","ext":"webm","filesize":3000},
	 {"format_id":"18","height":360,"width":640,"vcodec":"avc1.42001E","acodec":"mp4a.40.2","fps":30,"filesize_approx":5000},
	 {"format_id":"137","height":1920,"width":1080,"vcodec":"avc1.640028","acodec":"none","fps":30,"filesize":40000},
	 {"format_id":"248","height":1920,"width":1080,"vcodec":"vp9","acodec":"none","fps":60,"filesize":90000},
	 {"format_id":"247","height":1280,"width":720,"vcodec":"vp9","acodec":"none","fps":59.94,"tbr":8}],
	"subtitles":{"id":[],"en":[],"live_chat":[]},
	"automatic_captions":{"ja-orig":[],"ja":[],"en":[],"id":[],"fr":[]},
	"chapters":[{"start_time":0,"end_time":40,"title":"Intro"},{"start_time":40,"end_time":100,"title":"Main"}]}`
	in, err := summarize([]byte(raw), true)
	if err != nil {
		t.Fatal(err)
	}
	// 1080: H.264 beats the bigger VP9, plus m4a audio. 720: 8 kbit/s × 100 s = 100000 B, plus audio. 360 already has sound.
	wantV := []Variant{{1080, 30, 41000}, {720, 60, 101000}, {360, 30, 5000}}
	if !reflect.DeepEqual(in.Variants, wantV) {
		t.Errorf("variants %+v", in.Variants)
	}
	if in.Title != "T" || in.Site != "Youtube" || in.Date != "2024-01-31" || in.Views != 1234 || in.Formats != 7 || in.Playlist || in.AudioSize != 1000 {
		t.Errorf("%+v", in)
	}
	if !reflect.DeepEqual(in.Subs, []string{"en", "id"}) || !reflect.DeepEqual(in.AutoSubs, []string{"ja"}) || len(in.Chapters) != 2 || in.Chapters[1].Title != "Main" {
		t.Errorf("subs %v auto %v chapters %v", in.Subs, in.AutoSubs, in.Chapters)
	}
	// Without ffmpeg only the ready-made 360p file is on offer.
	if in, _ := summarize([]byte(raw), false); !reflect.DeepEqual(in.Variants, []Variant{{360, 30, 5000}}) {
		t.Errorf("no-ffmpeg variants %+v", in.Variants)
	}

	pl, _ := summarize([]byte(`{"_type":"playlist","title":"P","playlist_count":40,"entries":[
	  {"title":"A","duration":61.5,"thumbnails":[{"url":"https://t/a"}]},null,{"title":"C"}]}`), true)
	if !pl.Playlist || pl.Count != 40 || len(pl.Entries) != 3 || pl.Entries[0].Thumb != "https://t/a" || pl.Entries[2].Title != "C" {
		t.Fatalf("%+v", pl)
	}
}

// TestEngine drives the real yt-dlp through buildArgs and parseLine.
// Run with TARIK_ENGINE_DIR=<yt-dlp checkout> TARIK_TEST_URL=<direct video URL>.
func TestEngine(t *testing.T) {
	src, url := os.Getenv("TARIK_ENGINE_DIR"), os.Getenv("TARIK_TEST_URL")
	if src == "" || url == "" {
		t.Skip("set TARIK_ENGINE_DIR and TARIK_TEST_URL")
	}
	ffmpeg, _ := exec.LookPath("ffmpeg")
	run := func(o Options) (file string, sawProgress bool, steps []string) {
		t.Helper()
		o.URL, o.Dir = url, t.TempDir()
		// A config file planted in the working folder must be ignored: this one would run a command.
		cwd, marker := t.TempDir(), filepath.Join(o.Dir, "PLANTED")
		os.WriteFile(filepath.Join(cwd, "yt-dlp.conf"), []byte(`--exec "touch '`+marker+`'"`+"\n"), 0o644)
		defer func() {
			if _, err := os.Stat(marker); err == nil {
				t.Error("a planted yt-dlp.conf was obeyed")
			}
		}()
		c := exec.Command("python3", append([]string{"-m", "yt_dlp"}, engineArgs(append([]string{"--proxy", ""}, buildArgs(o, ffmpeg, "")...)...)...)...)
		c.Env, c.Dir = append(os.Environ(), "PYTHONPATH="+src), cwd
		out, _ := c.StdoutPipe()
		c.Stderr = c.Stdout
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			p, ok := parseLine(sc.Text())
			if !ok {
				t.Logf("unparsed: %s", sc.Text())
				continue
			}
			if p.Error != "" {
				t.Errorf("engine error: %s", p.Error)
			}
			if p.File != "" {
				file = p.File
			}
			if p.Stage != "" {
				steps = append(steps, p.Stage)
			}
			sawProgress = sawProgress || p.Percent == 100
		}
		if err := c.Wait(); err != nil {
			t.Errorf("exit: %v", err)
		}
		if _, err := os.Stat(file); err != nil {
			t.Errorf("reported file %q: %v", file, err)
		}
		return
	}
	duration := func(file string) string {
		out, _ := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", file).Output()
		return strings.TrimSpace(string(out))
	}

	if f, ok, _ := run(Options{Res: 1080, Embed: true, Name: "id"}); !ok || filepath.Ext(f) != ".mp4" || !strings.Contains(f, "[") {
		t.Errorf("video: %q progress=%v", f, ok)
	}
	if f, _, st := run(Options{Kind: "audio", Audio: "mp3", Embed: true}); filepath.Ext(f) != ".mp3" || len(st) == 0 || st[0] != "Converting audio" {
		t.Errorf("audio: %q steps=%v", f, st)
	}
	if f, _, _ := run(Options{Kind: "audio", Audio: "wav", Embed: true}); filepath.Ext(f) != ".wav" {
		t.Errorf("wav: %q", f)
	}
	if f, _, _ := run(Options{Start: 5, End: 10}); !strings.Contains(f, "[0m05s-0m10s]") || !strings.HasPrefix(duration(f), "5.0") {
		t.Errorf("trim: %q lasts %s s", f, duration(f))
	}
}
