package main

// Platform-neutral yt-dlp glue: argument building, output parsing, info summary.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Options struct {
	URL      string   `json:"url"`
	Dir      string   `json:"dir"`
	Kind     string   `json:"kind"`  // video | audio
	Res      int      `json:"res"`   // smaller side in px; 0 = best available
	Audio    string   `json:"audio"` // mp3 | m4a | opus | wav
	Subs     []string `json:"subs"`  // subtitle language codes
	Start    float64  `json:"start"` // trim, seconds
	End      float64  `json:"end"`   // 0 = whole video
	Sponsor  bool     `json:"sponsor"`
	Embed    bool     `json:"embed"` // cover art + tags
	Playlist bool     `json:"playlist"`
	Items    string   `json:"items"`   // playlist positions, e.g. "1-3,7"
	Name     string   `json:"name"`    // title | channel | id
	Rate     string   `json:"rate"`    // "" | 500K | 2M
	Cookies  string   `json:"cookies"` // "" or a browser to borrow the login from
}

var (
	reLang   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,19}$`)
	reItems  = regexp.MustCompile(`^[0-9][0-9,-]*$`)
	reRate   = regexp.MustCompile(`^[0-9]{1,5}[KM]$`)
	browsers = map[string]bool{"firefox": true, "edge": true, "chrome": true, "brave": true, "opera": true, "vivaldi": true}
	audios   = map[string]string{"mp3": "ba/b", "m4a": "ba[ext=m4a]/ba/b", "opus": "ba[acodec=opus]/ba/b", "wav": "ba/b"}
	names    = map[string]string{
		"title":   "%(title).150B",
		"channel": "%(uploader,channel|Unknown).60B - %(title).150B",
		"id":      "%(title).150B [%(id)s]",
	}
)

// clean drops anything the UI should never have sent, so every value that reaches
// the yt-dlp command line is one of ours.
func (o Options) clean() Options {
	if o.Kind != "audio" {
		o.Kind = "video"
	}
	if _, ok := audios[o.Audio]; !ok {
		o.Audio = "mp3"
	}
	if _, ok := names[o.Name]; !ok {
		o.Name = "title"
	}
	if o.Res < 0 || o.Res > 4320 {
		o.Res = 0
	}
	var subs []string
	for _, l := range o.Subs {
		if reLang.MatchString(l) && len(subs) < 8 {
			subs = append(subs, l)
		}
	}
	o.Subs = subs
	if !reItems.MatchString(o.Items) || len(o.Items) > 4000 {
		o.Items = ""
	}
	if !reRate.MatchString(o.Rate) {
		o.Rate = ""
	}
	if !browsers[o.Cookies] {
		o.Cookies = ""
	}
	if o.Start < 0 || o.End <= o.Start || o.Playlist {
		o.Start, o.End = 0, 0
	}
	return o
}

// Our own machine-readable lines; see the templates in buildArgs.
const (
	tagProgress = "[tarik] "
	tagStep     = "[tarik-pp] "
	tagItem     = "[tarik-item] "
	tagFile     = "[tarik-file] "
)

// engineArgs is how every yt-dlp run starts. yt-dlp reads yt-dlp.conf from its working folder and a few
// other places, and a config file can carry --exec; a stray or planted one must never steer Tarik.
func engineArgs(args ...string) []string { return append([]string{"--ignore-config"}, args...) }

// playable reports whether a finished download is safe to hand to the Windows shell to open.
func playable(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".mkv", ".webm", ".mov", ".m4v", ".avi", ".flv", ".3gp", ".ts",
		".mp3", ".m4a", ".opus", ".ogg", ".oga", ".wav", ".flac", ".aac", ".weba", ".mka":
		return true
	}
	return false
}

// baseArgs are shared by look-ups and downloads. YouTube needs a JS runtime (Deno) to
// unlock its full-quality streams; without one yt-dlp falls back to a few formats.
func baseArgs(ffmpeg, deno, cookies string) []string {
	a := []string{"--encoding", "utf-8", "--color", "never", "--no-warnings"}
	if deno != "" {
		a = append(a, "--js-runtimes", "deno:"+deno)
	}
	if ffmpeg != "" {
		a = append(a, "--ffmpeg-location", ffmpeg)
	}
	if browsers[cookies] {
		a = append(a, "--cookies-from-browser", cookies)
	}
	return a
}

func probeArgs(url, ffmpeg, deno, cookies string) []string {
	return append(baseArgs(ffmpeg, deno, cookies), "-J", "--flat-playlist", "--no-playlist", "--", url)
}

// buildArgs turns UI choices into yt-dlp arguments. Everything that needs ffmpeg
// (merging, converting, trimming, embedding) is left out when ffmpeg is missing.
func buildArgs(o Options, ffmpeg, deno string) []string {
	o = o.clean()
	ff := ffmpeg != ""
	a := append(baseArgs(ffmpeg, deno, o.Cookies),
		// --quiet leaves only our own lines and errors on the pipe.
		"--quiet", "--progress", "--newline", "--no-simulate", "--no-mtime", "-N", "4",
		"--progress-template", "download:"+tagProgress+"%(progress.status)s|%(progress._percent)s|%(progress.total_bytes)s|%(progress.total_bytes_estimate)s|%(progress.speed)s|%(progress.eta)s|%(progress.downloaded_bytes)s|%(info.vcodec)s",
		"--progress-template", "postprocess:"+tagStep+"%(progress.postprocessor)s|%(progress.status)s",
		"--print", "before_dl:"+tagItem+"%(.{title,thumbnail,playlist_index,playlist_count})j",
		"--print", "after_move:"+tagFile+"%(filepath)j")

	name := names[o.Name]
	trim := ff && o.End > o.Start
	if trim {
		a = append(a, "--download-sections", fmt.Sprintf("*%.2f-%.2f", o.Start, o.End), "--force-keyframes-at-cuts")
		name += " [" + stamp(o.Start) + "-" + stamp(o.End) + "]" // keeps the clip from overwriting the full video
	}
	name += ".%(ext)s"
	if o.Playlist {
		a = append(a, "--yes-playlist")
		if o.Items != "" {
			a = append(a, "--playlist-items", o.Items)
		}
		name = filepath.Join("%(playlist_title,playlist_id|Playlist).80B", "%(playlist_index)s - "+name)
	} else {
		a = append(a, "--no-playlist")
	}
	a = append(a, "-o", filepath.Join(o.Dir, name))
	if o.Rate != "" {
		a = append(a, "--limit-rate", o.Rate)
	}
	if ff && o.Sponsor && !trim {
		a = append(a, "--sponsorblock-remove", "sponsor")
	}
	if ff && o.Embed {
		a = append(a, "--embed-metadata")
		if o.Kind != "audio" || o.Audio != "wav" { // WAV has no place for cover art; yt-dlp fails on it
			a = append(a, "--embed-thumbnail")
		}
	}

	if o.Kind == "audio" {
		if ff {
			a = append(a, "-f", audios[o.Audio], "-x", "--audio-format", o.Audio, "--audio-quality", "0")
		} else {
			a = append(a, "-f", "ba[ext=m4a]/ba/b")
		}
		return append(a, "--", o.URL)
	}

	if len(o.Subs) > 0 {
		a = append(a, "--write-subs", "--write-auto-subs", "--sub-langs", strings.Join(o.Subs, ","))
		if ff {
			a = append(a, "--embed-subs")
		}
	}
	// "res" is the smaller side, so 1080 means 1080p for vertical Shorts too.
	res := "res"
	if o.Res > 0 {
		res += ":" + strconv.Itoa(o.Res)
	}
	// Prefer H.264 + AAC at equal resolution so the MP4 plays in any Windows player.
	a = append(a, "-S", res+",vcodec:h264,acodec:aac")
	if ff {
		a = append(a, "-f", "bv*+ba/b", "--merge-output-format", "mp4")
	} else {
		a = append(a, "-f", "b")
	}
	return append(a, "--", o.URL)
}

type Progress struct {
	Percent float64 `json:"percent"` // -1 when unknown
	Size    string  `json:"size,omitempty"`
	Got     string  `json:"got,omitempty"` // downloaded so far, when the total is unknown
	Speed   string  `json:"speed,omitempty"`
	Bps     float64 `json:"bps,omitempty"`
	ETA     string  `json:"eta,omitempty"`
	Part    string  `json:"part,omitempty"` // "video" | "audio" when the two arrive separately
	Stage   string  `json:"stage,omitempty"`
	File    string  `json:"file,omitempty"`
	Title   string  `json:"title,omitempty"`
	Thumb   string  `json:"thumb,omitempty"`
	Index   int     `json:"index,omitempty"` // position in a playlist
	Count   int     `json:"count,omitempty"`
	Error   string  `json:"error,omitempty"`
}

// What the user sees while yt-dlp's post-processors run. Unlisted ones stay silent.
var steps = map[string]string{
	"Merger":             "Joining video and audio",
	"ExtractAudio":       "Converting audio",
	"VideoConvertor":     "Converting video",
	"VideoRemuxer":       "Repacking video",
	"EmbedSubtitle":      "Adding subtitles",
	"EmbedThumbnail":     "Adding cover art",
	"Metadata":           "Writing tags",
	"SponsorBlock":       "Looking for sponsor segments",
	"ModifyChapters":     "Removing sponsor segments",
	"SubtitlesConvertor": "Converting subtitles",
}

// parseLine reads one line of yt-dlp output. ok=false means nothing worth showing.
func parseLine(s string) (p Progress, ok bool) {
	s = strings.TrimRight(s, "\r\n ")
	p.Percent = -1
	switch {
	case strings.HasPrefix(s, "ERROR: "):
		p.Error = strings.TrimPrefix(s, "ERROR: ")
	case strings.HasPrefix(s, tagProgress):
		f := strings.Split(strings.TrimPrefix(s, tagProgress), "|")
		if len(f) != 8 {
			return p, false
		}
		num := func(i int) float64 {
			v, err := strconv.ParseFloat(f[i], 64)
			if err != nil {
				return -1 // "NA"
			}
			return v
		}
		if v := num(1); v >= 0 {
			p.Percent = v
		}
		if f[0] == "finished" {
			p.Percent = 100
		}
		if t := num(2); t > 0 {
			p.Size = bytes(t)
		} else if t := num(3); t > 0 {
			p.Size = "~" + bytes(t)
		} else if g := num(6); g > 0 {
			p.Got = bytes(g)
		}
		if v := num(4); v > 0 {
			p.Speed, p.Bps = bytes(v)+"/s", v
		}
		if v := num(5); v >= 0 && f[0] != "finished" {
			p.ETA = clock(v)
		}
		switch f[7] {
		case "NA":
		case "none":
			p.Part = "audio"
		default:
			p.Part = "video"
		}
	case strings.HasPrefix(s, tagStep):
		f := strings.Split(strings.TrimPrefix(s, tagStep), "|")
		if len(f) != 2 || f[1] != "started" {
			return p, false
		}
		if p.Stage = steps[f[0]]; p.Stage == "" {
			if !strings.HasPrefix(f[0], "Fixup") {
				return p, false
			}
			p.Stage = "Tidying the file"
		}
	case strings.HasPrefix(s, tagItem):
		var it struct {
			Title, Thumbnail string
			Index            int `json:"playlist_index"`
			Count            int `json:"playlist_count"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(s, tagItem)), &it) != nil {
			return p, false
		}
		p.Title, p.Thumb, p.Index, p.Count = it.Title, it.Thumbnail, it.Index, it.Count
	case strings.HasPrefix(s, tagFile):
		if json.Unmarshal([]byte(strings.TrimPrefix(s, tagFile)), &p.File) != nil || p.File == "" {
			return p, false
		}
	default:
		return p, false
	}
	return p, true
}

// bytes formats like Windows Explorer: binary units, "MB" labels.
func bytes(n float64) string {
	u := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	for n >= 1024 && i < len(u)-1 {
		n /= 1024
		i++
	}
	if i < 2 {
		return fmt.Sprintf("%.0f %s", n, u[i])
	}
	return fmt.Sprintf("%.1f %s", n, u[i])
}

func clock(sec float64) string {
	s := int(sec)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s%3600/60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// stamp is a time that is legal in a Windows file name: 1h02m03s, 0m12s.
func stamp(sec float64) string {
	s := int(sec)
	if s >= 3600 {
		return fmt.Sprintf("%dh%02dm%02ds", s/3600, s%3600/60, s%60)
	}
	return fmt.Sprintf("%dm%02ds", s/60, s%60)
}

type Variant struct {
	Res  int   `json:"res"` // smaller side, the "p" number
	FPS  int   `json:"fps,omitempty"`
	Size int64 `json:"size,omitempty"` // estimated bytes of the finished file
}

type Chapter struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Title string  `json:"title"`
}

type Entry struct {
	Title    string  `json:"title"`
	Duration float64 `json:"duration,omitempty"`
	Thumb    string  `json:"thumb,omitempty"`
}

type Info struct {
	Title       string    `json:"title"`
	Uploader    string    `json:"uploader,omitempty"`
	Site        string    `json:"site,omitempty"`
	Duration    float64   `json:"duration,omitempty"`
	Views       int64     `json:"views,omitempty"`
	Likes       int64     `json:"likes,omitempty"`
	Date        string    `json:"date,omitempty"`
	Thumbnail   string    `json:"thumbnail,omitempty"`
	URL         string    `json:"url,omitempty"`
	Description string    `json:"description,omitempty"`
	Variants    []Variant `json:"variants,omitempty"` // resolutions on offer, high to low
	AudioSize   int64     `json:"audioSize,omitempty"`
	Subs        []string  `json:"subs,omitempty"`     // human-made subtitle languages
	AutoSubs    []string  `json:"autoSubs,omitempty"` // machine captions worth offering
	Chapters    []Chapter `json:"chapters,omitempty"`
	Formats     int       `json:"formats"`
	Playlist    bool      `json:"playlist"`
	Count       int       `json:"count,omitempty"`
	Entries     []Entry   `json:"entries,omitempty"`
	Live        bool      `json:"live,omitempty"`
}

type format struct {
	Height, Width  float64
	Vcodec, Acodec string
	Ext            string
	FPS, Tbr       float64
	Filesize       float64
	Approx         float64 `json:"filesize_approx"`
}

func (f format) video() bool { return f.Vcodec != "none" && f.Height > 0 }
func (f format) audio() bool { return f.Acodec != "none" }
func (f format) res() int {
	if f.Width > 0 && f.Width < f.Height {
		return int(f.Width) // vertical video: 1080x1920 is "1080p", same as yt-dlp's res
	}
	return int(f.Height)
}
func (f format) size(duration float64) float64 {
	switch {
	case f.Filesize > 0:
		return f.Filesize
	case f.Approx > 0:
		return f.Approx
	}
	return f.Tbr * 125 * duration // kbit/s → bytes
}

// summarize keeps what the UI shows from yt-dlp's (often 500 KB) -J output.
// ffmpeg tells it whether separate video and audio streams can be joined.
func summarize(raw []byte, ffmpeg bool) (Info, error) {
	var j struct {
		Type         string `json:"_type"`
		Title        string
		Uploader     string
		Channel      string
		Thumbnail    string
		Description  string
		ExtractorKey string  `json:"extractor_key"`
		UploadDate   string  `json:"upload_date"`
		WebpageURL   string  `json:"webpage_url"`
		ViewCount    float64 `json:"view_count"`
		LikeCount    float64 `json:"like_count"`
		Count        int     `json:"playlist_count"`
		IsLive       bool    `json:"is_live"`
		Duration     float64
		Thumbnails   []struct{ URL string }
		Formats      []format
		Subtitles    map[string]json.RawMessage
		Auto         map[string]json.RawMessage `json:"automatic_captions"`
		Chapters     []struct {
			Start float64 `json:"start_time"`
			End   float64 `json:"end_time"`
			Title string
		}
		Entries []json.RawMessage
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return Info{}, err
	}
	in := Info{Title: j.Title, Uploader: j.Uploader, Site: j.ExtractorKey, Duration: j.Duration,
		Views: int64(j.ViewCount), Likes: int64(j.LikeCount), Thumbnail: j.Thumbnail, URL: j.WebpageURL,
		Formats: len(j.Formats), Playlist: j.Type == "playlist", Count: j.Count, Live: j.IsLive}
	if in.Uploader == "" {
		in.Uploader = j.Channel
	}
	if in.Thumbnail == "" && len(j.Thumbnails) > 0 {
		in.Thumbnail = j.Thumbnails[len(j.Thumbnails)-1].URL
	}
	if d := j.UploadDate; len(d) == 8 {
		in.Date = d[:4] + "-" + d[4:6] + "-" + d[6:]
	}
	if r := []rune(strings.TrimSpace(j.Description)); len(r) > 280 {
		in.Description = string(r[:280]) + "…"
	} else {
		in.Description = string(r)
	}

	// The audio track a merged download gets: AAC (m4a) when offered, else the biggest.
	var audio float64
	m4a := false
	for _, f := range j.Formats {
		if f.video() || !f.audio() {
			continue
		}
		if s, isM4a := f.size(j.Duration), f.Ext == "m4a"; (isM4a && !m4a) || (isM4a == m4a && s > audio) {
			audio, m4a = s, isM4a
		}
	}
	in.AudioSize = int64(audio)

	// One line per resolution, picking the stream the download would: H.264 first, then fps, then size.
	type pick struct {
		f    format
		h264 bool
	}
	best := map[int]pick{}
	for _, f := range j.Formats {
		if !f.video() || (!ffmpeg && !f.audio()) {
			continue
		}
		c, old := pick{f, strings.HasPrefix(f.Vcodec, "avc")}, best[f.res()]
		if old.f.Height == 0 || c.h264 != old.h264 && c.h264 || c.h264 == old.h264 &&
			(c.f.FPS > old.f.FPS || c.f.FPS == old.f.FPS && c.f.size(j.Duration) > old.f.size(j.Duration)) {
			best[f.res()] = c
		}
	}
	for r, c := range best {
		size := c.f.size(j.Duration)
		if size > 0 && !c.f.audio() {
			size += audio
		}
		in.Variants = append(in.Variants, Variant{Res: r, FPS: int(c.f.FPS + 0.5), Size: int64(size)})
	}
	sort.Slice(in.Variants, func(a, b int) bool { return in.Variants[a].Res > in.Variants[b].Res })

	for l := range j.Subtitles {
		if l != "live_chat" && reLang.MatchString(l) {
			in.Subs = append(in.Subs, l)
		}
	}
	sort.Strings(in.Subs)
	if len(in.Subs) > 24 {
		in.Subs = in.Subs[:24]
	}
	// YouTube lists a machine translation into every language; offer only the spoken one, English and Indonesian.
	seen := map[string]bool{}
	for _, l := range in.Subs {
		seen[l] = true
	}
	var auto []string
	for l := range j.Auto {
		if base, orig := strings.CutSuffix(l, "-orig"); orig || l == "en" || l == "id" {
			if !seen[base] && reLang.MatchString(base) {
				seen[base] = true
				auto = append(auto, base)
			}
		}
	}
	sort.Strings(auto)
	in.AutoSubs = auto

	for i, c := range j.Chapters {
		if i < 200 && c.End > c.Start {
			in.Chapters = append(in.Chapters, Chapter{c.Start, c.End, c.Title})
		}
	}
	for i, raw := range j.Entries {
		if i >= 1000 { // shortcut: longer playlists still download in full, they just can't be hand-picked past 1000
			break
		}
		var e struct {
			Title      string
			Duration   float64
			Thumbnails []struct{ URL string }
		}
		json.Unmarshal(raw, &e) // a malformed entry still keeps its slot, so positions stay right
		en := Entry{Title: e.Title, Duration: e.Duration}
		if len(e.Thumbnails) > 0 {
			en.Thumb = e.Thumbnails[0].URL
		}
		in.Entries = append(in.Entries, en)
	}
	if in.Count == 0 {
		in.Count = len(j.Entries)
	}
	return in, nil
}
