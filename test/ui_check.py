"""Drive the Tarik UI in Chromium with a fake Go bridge: assert behaviour, save screenshots.

Needs: pip install playwright && playwright install chromium.  Run: python test/ui_check.py
"""
import asyncio, base64, json, pathlib, sys
from playwright.async_api import async_playwright

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parent / "ui"
OUT = HERE / "out"
OUT.mkdir(exist_ok=True)
b64 = lambda p: base64.b64encode((ROOT / "fonts" / p).read_bytes()).decode()
PAGE = OUT / "built.html"   # what main.go does at start-up: fill in the fonts and the script nonce
PAGE.write_text((ROOT / "index.html").read_text().replace("{{FONT_DISPLAY}}", b64("archivo.woff2")).replace("{{FONT_MONO}}", b64("geist-mono.woff2")).replace("{{NONCE}}", "dGVzdG5vbmNl"))

THUMB = "data:image/svg+xml," + "%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 9'%3E%3Crect width='16' height='9' fill='%23364a3a'/%3E%3Ccircle cx='11' cy='4' r='2.4' fill='%23e8c36a'/%3E%3Cpath d='M0 9l5-5 4 3 3-2 4 4z' fill='%231d2a21'/%3E%3C/svg%3E"
VIDEO = {"title": "Making a desk lamp from scratch — walnut, brass and one very patient afternoon", "uploader": "Studio Notes", "site": "Youtube", "duration": 1012,
         "views": 1834000, "likes": 52000, "date": "2026-09-14", "thumbnail": THUMB, "formats": 38, "playlist": False,
         "description": "A slow build from rough lumber to a finished lamp. Tools, joinery and the wiring all explained along the way.",
         "variants": [{"res": 2160, "fps": 60, "size": 1_420_000_000}, {"res": 1440, "fps": 60, "size": 780_000_000}, {"res": 1080, "fps": 60, "size": 327_500_000},
                      {"res": 720, "fps": 30, "size": 141_000_000}, {"res": 480, "fps": 30, "size": 72_000_000}, {"res": 360, "fps": 30, "size": 41_000_000}, {"res": 144, "fps": 15, "size": 9_000_000}],
         "audioSize": 16_300_000, "subs": ["en", "id"], "autoSubs": ["ja"],
         "chapters": [{"start": 0, "end": 95, "title": "Rough lumber"}, {"start": 95, "end": 410, "title": "Joinery"}, {"start": 410, "end": 760, "title": "Brass work"}, {"start": 760, "end": 1012, "title": "Wiring and finish"}]}
PLAYLIST = {"title": "Shop basics", "uploader": "Studio Notes", "site": "YoutubeTab", "playlist": True, "count": 6, "thumbnail": THUMB, "formats": 0,
            "entries": [{"title": f"Episode {n}: {t}", "duration": 300 + n * 47} for n, t in enumerate(["Sharpening", "Marking out", "Sawing square", "Glue-ups", "Finishing oil", "Shop tour"], 1)]}

MOCK = """
window.__dl=[]; window.__saved={}; window.__probe=null; let n=0;
const ev=(k,d,t=0)=>setTimeout(()=>window.onGo(k,d),t);
window.__ev=ev; window.__clip='';
window.goBoot=async()=>{ev('engine',{ok:true,version:'2026.11.04',ffmpeg:FF,path:'C:\\\\x\\\\yt-dlp.exe',deno:'C:\\\\x\\\\deno.exe',denoState:'ready'},30);
  return {settings:SETTINGS,history:HISTORY,defaultDir:'C:\\\\Users\\\\you\\\\Downloads'}};
window.goSave=async s=>{window.__saved.settings=s}; window.goSaveHistory=async s=>{window.__saved.history=s};
window.goClipboard=async()=>window.__clip;
window.goProbe=async(id,u,c)=>{window.__probe={u,c,id}};
window.goDownload=async(id,o)=>{window.__dl.push({id,o})};
window.goCancel=async id=>ev('progress',{id,done:true,cancelled:true},20);
for (const f of ['goPickFolder','goReveal','goOpen','goUpdate','goRetryDeno','goTitleBar']) window[f]=async(...a)=>{(window.__calls??=[]).push([f,...a])};
window.goExtractors=async()=>ev('extractors',{list:['abc.net.au','Bandcamp','BBC','BiliBili','Dailymotion','Facebook','Instagram','SoundCloud','TikTok','Twitch:vod','Vimeo','youtube']},30);
"""
HIST = [{"t": "Kumiko panel in an afternoon", "th": THUMB, "u": "https://youtu.be/kumiko", "f": "D:\\Videos\\Kumiko panel in an afternoon.mp4", "d": "D:\\Videos", "w": 1794300000000, "q": "1080p60 MP4", "z": "212.4 MB", "n": 0},
        {"t": "Shop basics", "th": THUMB, "u": "https://youtube.com/playlist?list=PL1", "f": "D:\\Videos\\Shop basics\\6 - Shop tour.mp3", "d": "D:\\Videos", "w": 1794200000000, "q": "MP3", "z": "", "n": 6}]
fails = []
def check(name, cond, detail=""):
    print(("ok   " if cond else "FAIL ") + name + (f"  [{detail}]" if detail and not cond else ""))
    if not cond: fails.append(name)

async def page_for(b, scheme="dark", ffmpeg=True, settings=None, history=None, size=(1000, 790)):
    pg = await b.new_page(viewport={"width": size[0], "height": size[1]}, color_scheme=scheme)
    pg.errs = []
    pg.on("pageerror", lambda e: pg.errs.append(str(e)))
    pg.on("console", lambda m: m.type == "error" and pg.errs.append(m.text))
    await pg.add_init_script(MOCK.replace("FF", "'C:\\\\ffmpeg\\\\ffmpeg.exe'" if ffmpeg else "''")
                             .replace("SETTINGS", json.dumps(json.dumps(settings or {}))).replace("HISTORY", json.dumps(json.dumps(history or []))))
    await pg.goto(PAGE.as_uri())
    await pg.wait_for_timeout(250)
    return pg

async def look(pg, url, info=None, error=None):
    await pg.fill("#url", url); await pg.press("#url", "Enter"); await pg.wait_for_timeout(60)
    pid = await pg.evaluate("window.__probe.id")
    await pg.evaluate("([id,info,error])=>window.onGo('info',{id,info,error})", [pid, info, error])
    await pg.wait_for_timeout(120)

async def main():
    async with async_playwright() as p:
        b = await p.chromium.launch()

        # ---- 1. single video, dark ----
        pg = await page_for(b, history=HIST)
        await pg.screenshot(path=OUT / "01-idle-dark.png")
        check("idle shown, bar counts history", await pg.is_visible("#idle") and "2 finished" in await pg.inner_text("#barTxt"))
        await look(pg, "https://www.youtube.com/watch?v=abc", VIDEO)
        check("probe got url + cookies arg", await pg.evaluate("window.__probe.u") == "https://www.youtube.com/watch?v=abc" and await pg.evaluate("window.__probe.c") == "")
        chips = await pg.locator('[data-k="res"] .chip').all_inner_texts()
        check("quality chips skip 144p, show sizes", len(chips) == 7 and "144p" not in " ".join(chips) and "312.3 MB" in " ".join(chips), str(chips))
        check("summary: best = 2160p60", "2160p60 MP4" in await pg.inner_text("#sum") and "1.3 GB" in await pg.inner_text("#sum"), await pg.inner_text("#sum"))
        await pg.click('[data-k="res"] [data-v="1080"]')
        check("1080 picked + saved", "1080p60 MP4 · about 312.3 MB" in await pg.inner_text("#sum") and '"res":1080' in await pg.evaluate("window.__saved.settings"), await pg.inner_text("#sum"))
        await pg.keyboard.press("ArrowRight")
        foc = await pg.evaluate("document.activeElement.dataset.v")
        check("arrow key moves radio and keeps focus", foc == "720" and "720p MP4" in await pg.inner_text("#sum"), foc)
        await pg.keyboard.press("ArrowLeft")
        await pg.click('[data-sub="id"]'); await pg.click('[data-tog="embed"]')
        check("subtitle + embed toggles", await pg.get_attribute('[data-sub="id"]', "aria-pressed") == "true" and "1 subtitle" in await pg.inner_text("#sum"))
        await pg.screenshot(path=OUT / "02-video-dark.png")
        await pg.set_viewport_size({"width": 1000, "height": 600}); await pg.evaluate("document.querySelector('#stage').scrollTo(0, 0)"); await pg.wait_for_timeout(50)
        gap = await pg.evaluate("innerHeight - document.querySelector('.act').getBoundingClientRect().bottom")
        check("download bar sticks just above the tray", abs(gap - 56) < 1.5, str(gap))
        await pg.screenshot(path=OUT / "02b-sticky-short-window.png")
        await pg.evaluate("document.querySelector('#stage').scrollTo(0, 9999)"); await pg.wait_for_timeout(50)
        lastRow = await pg.evaluate("document.querySelector('.prow:last-child').getBoundingClientRect().bottom - document.querySelector('.act').getBoundingClientRect().top")
        check("scrolled to the end, the bar covers nothing", lastRow <= 0.5, str(lastRow))
        await pg.set_viewport_size({"width": 1000, "height": 790}); await pg.wait_for_timeout(50)

        # trim: drag the end grip to 50%, keyboard on the start grip, click a chapter
        box = await pg.locator("#track").bounding_box(); gb = await pg.locator("#gB").bounding_box()
        await pg.mouse.move(gb["x"] + gb["width"] / 2 + 3, gb["y"] + 10); await pg.mouse.down()
        await pg.mouse.move(box["x"] + box["width"] / 2 + 3, gb["y"] + 10, steps=6); await pg.mouse.up()
        t1 = await pg.evaluate("S.trim[1]")
        check("drag end grip to the middle (grab offset kept)", abs(t1 - 506) < 2, str(t1))
        await pg.focus("#gA"); await pg.keyboard.press("Shift+ArrowRight"); await pg.keyboard.press("ArrowRight")
        check("grip keyboard: +10 +1 s, focus stays", await pg.evaluate("S.trim[0]") == 11 and await pg.evaluate("document.activeElement.id") == "gA")
        check("clip summary + sponsor disabled", "clip 0:11 to 8:26" in await pg.inner_text("#sum") and await pg.is_disabled('[data-tog="sponsor"]'), await pg.inner_text("#sum"))
        await pg.fill("#tOut", "1:35"); await pg.press("#tOut", "Enter")
        check("typed end time", await pg.evaluate("S.trim[1]") == 95)
        await pg.click('.chap[data-c="2"]')
        check("chapter click selects it", await pg.evaluate("S.trim") == [410, 760] and "keeps 5:50" in await pg.inner_text("#tLen"), await pg.inner_text("#tLen"))
        await pg.screenshot(path=OUT / "03-trim-dark.png")

        await pg.click("#dlBtn"); await pg.wait_for_timeout(100)
        o = (await pg.evaluate("window.__dl"))[0]["o"]
        check("download options", o["res"] == 1080 and o["subs"] == ["id"] and o["embed"] and (o["start"], o["end"]) == (410, 760) and o["dir"].endswith("Downloads") and not o["playlist"], json.dumps(o))
        check("bar shows running", "1 running" in await pg.inner_text("#barTxt"))
        did = (await pg.evaluate("window.__dl"))[0]["id"]
        send = lambda d: pg.evaluate("d=>window.onGo('progress',d)", d)
        await pg.click("#bar"); await pg.wait_for_timeout(700)
        check("tray opened by click", await pg.get_attribute("#bar", "aria-expanded") == "true" and abs(await pg.evaluate("tray.y")) < 1)
        check("clip job starts as cutting", "cutting the clip" in await pg.inner_text(".row .rs"), await pg.inner_text(".row .rs"))
        await send({"id": did, "p": {"percent": 42.7, "size": "312.4 MB", "speed": "6.2 MB/s", "bps": 6500000, "eta": "0:29", "part": "video"}})
        await pg.wait_for_timeout(400)
        row = await pg.inner_text(".row .rs")
        check("progress line", row == "video · 42.7% of 312.4 MB · 6.2 MB/s · 0:29 left" and "6.2 MB/s" in await pg.inner_text("#barSpeed"), row)
        await pg.screenshot(path=OUT / "04-tray-dark.png")
        await send({"id": did, "p": {"percent": -1, "stage": "Joining video and audio"}})
        check("stage line + speed cleared", "Joining video and audio…" in await pg.inner_text(".row .rs") and await pg.inner_text("#barSpeed") == "")
        await send({"id": did, "done": True, "file": "C:\\Users\\you\\Downloads\\Lamp [6m50s-12m40s].mp4", "saved": 1, "bytes": 88_000_000})
        await pg.wait_for_timeout(100)
        h = json.loads(await pg.evaluate("window.__saved.history"))
        check("finished job moves to saved history", len(h) == 3 and h[0]["q"] == "1080p60 MP4 clip" and h[0]["z"] == "83.9 MB" and "3 finished" in await pg.inner_text("#barTxt"), json.dumps(h[0]))
        await pg.fill("#find", "kumiko"); await pg.wait_for_timeout(50)
        check("history search", await pg.locator("#list .row").count() == 1)
        await pg.fill("#find", "")
        # drag the tray shut with a throw
        bb = await pg.locator("#bar").bounding_box()
        await pg.mouse.move(bb["x"] + 200, bb["y"] + 20); await pg.mouse.down()
        await pg.mouse.move(bb["x"] + 200, bb["y"] + 90, steps=3); await pg.wait_for_timeout(400)   # a slow, short pull...
        check("tray follows the pointer 1:1", abs(await pg.evaluate("tray.y") - 70) < 1, str(await pg.evaluate("tray.y")))
        await pg.mouse.up(); await pg.wait_for_timeout(900)
        check("...springs back open", await pg.get_attribute("#bar", "aria-expanded") == "true" and abs(await pg.evaluate("tray.y")) < 1)
        await pg.mouse.move(bb["x"] + 200, bb["y"] + 20); await pg.mouse.down()
        for dy in (40, 70, 100, 130):   # ...a quick flick throws it shut
            await pg.mouse.move(bb["x"] + 200, bb["y"] + 20 + dy); await pg.wait_for_timeout(12)
        await pg.mouse.up(); await pg.wait_for_timeout(900)
        check("thrown tray closes and settles", await pg.get_attribute("#bar", "aria-expanded") == "false" and abs(await pg.evaluate("tray.y - shut()")) < 1, str(await pg.evaluate("tray.y - shut()")))
        check("closed tray is inert", await pg.evaluate("document.querySelector('#trayBody').inert"))
        check("no page errors (video flow)", not pg.errs, str(pg.errs))

        # ---- 2. batch + queue limit + failure + stop ----
        await pg.evaluate("navigator.clipboard")  # no-op
        await pg.evaluate("""()=>{const dt=new DataTransfer();dt.setData('text','https://youtu.be/a1\\nhttps://vimeo.com/22 and https://youtu.be/a3');
            document.querySelector('#url').dispatchEvent(new ClipboardEvent('paste',{clipboardData:dt,bubbles:true,cancelable:true}))}""")
        await pg.wait_for_timeout(100)
        check("batch card", "3 links ready" in await pg.inner_text("#media") and await pg.inner_text("#dlBtn") == "Download 3" and await pg.is_hidden("#trim"))
        await pg.screenshot(path=OUT / "05-batch-dark.png")
        await pg.evaluate("window.__dl.length=0"); await pg.click("#dlBtn"); await pg.wait_for_timeout(100)
        dl = await pg.evaluate("window.__dl")
        check("queue starts 2 of 3", len(dl) == 2 and "2 running · 1 waiting" in await pg.inner_text("#barTxt"), await pg.inner_text("#barTxt"))
        await send({"id": dl[0]["id"], "p": {"percent": -1, "title": "First real title", "thumb": THUMB}})
        await send({"id": dl[0]["id"], "done": True, "p": {"percent": -1, "error": "Video unavailable"}})
        await pg.wait_for_timeout(100)
        dl = await pg.evaluate("window.__dl")
        check("failure frees a slot, third starts", len(dl) == 3 and "2 running · 1 failed" in await pg.inner_text("#barTxt"), await pg.inner_text("#barTxt"))
        await pg.keyboard.press("Control+j"); await pg.wait_for_timeout(600)
        check("failed row shows title + error", "First real title" in await pg.inner_text("#list") and "Video unavailable" in await pg.inner_text("#list"))
        await pg.click('.row[data-m="live"] [data-a="stop"]'); await pg.wait_for_timeout(100)
        check("stop marks row stopped", "stopped" in await pg.inner_text("#list"))
        await pg.screenshot(path=OUT / "06-queue-dark.png")
        await pg.keyboard.press("Escape"); await pg.wait_for_timeout(500)
        check("no page errors (batch flow)", not pg.errs, str(pg.errs))

        # ---- 3. playlist ----
        await look(pg, "https://www.youtube.com/playlist?list=PL1", PLAYLIST)
        check("playlist list + count", await pg.locator("#plist input").count() == 6 and "6 of 6 chosen" in await pg.inner_text("#plCount") and await pg.inner_text("#dlBtn") == "Download 6")
        for n in (3, 5): await pg.click(f'#plist input[data-n="{n}"]')
        await pg.click('[data-k="kind"] [data-v="audio"]')
        await pg.evaluate("window.__dl.length=0"); await pg.click("#dlBtn"); await pg.wait_for_timeout(100)
        await pg.screenshot(path=OUT / "07-playlist-dark.png")
        # one slot is still held by the batch job above, so this may wait; free it
        o = None
        for _ in range(3):
            dl = await pg.evaluate("window.__dl")
            if dl: o = dl[0]["o"]; break
            await pg.evaluate("S.jobs.filter(j=>j.state==='running').forEach(j=>window.onGo('progress',{id:j.goId,done:true,cancelled:true}))"); await pg.wait_for_timeout(100)
        check("playlist options", o and o["playlist"] and o["items"] == "1-2,4,6" and o["kind"] == "audio" and o["start"] == 0, json.dumps(o))
        await send({"id": dl[0]["id"], "p": {"percent": -1, "title": "Episode 2: Marking out", "index": 2, "count": 4}})
        await send({"id": dl[0]["id"], "p": {"percent": 10, "size": "4.0 MB", "part": "audio"}})
        await pg.keyboard.press("Control+j"); await pg.wait_for_timeout(600)
        txt = await pg.locator('.row[data-m="live"]', has_text="Shop basics").inner_text()
        check("playlist row: position, current title, no part label for audio", "Shop basics — Episode 2: Marking out" in txt and "2/4 · 10.0% of 4.0 MB" in txt, txt)
        await send({"id": dl[0]["id"], "done": True, "file": "D:\\x\\4.mp3", "saved": 3, "p": {"percent": -1, "error": "Video 6 is private"}})
        await pg.wait_for_timeout(100)
        h = json.loads(await pg.evaluate("window.__saved.history"))[0]
        check("partial playlist kept with note", h["n"] == 3 and "private" in h["e"] and "Some failed" in await pg.inner_text("#list"), json.dumps(h))
        check("no page errors (playlist flow)", not pg.errs, str(pg.errs))
        await pg.close()

        # ---- 4. light theme, settings, accent, clipboard offer ----
        pg = await page_for(b, scheme="light", history=HIST)
        await pg.screenshot(path=OUT / "08-idle-light.png")
        await look(pg, "https://www.youtube.com/watch?v=abc", VIDEO)
        await pg.screenshot(path=OUT / "09-video-light.png")
        await pg.click("#setBtn"); await pg.wait_for_timeout(600)
        await pg.screenshot(path=OUT / "10-settings-light.png")
        await pg.click('[data-set="accent"] [data-v="blue"]'); await pg.click('[data-set="theme"] [data-v="dark"]'); await pg.click('[data-set="max"] [data-v="3"]')
        await pg.select_option('[data-set="cookies"]', "firefox"); await pg.select_option('[data-set="rate"]', "2M"); await pg.select_option('[data-set="name"]', "channel")
        st = json.loads(await pg.evaluate("window.__saved.settings"))
        check("settings saved", (st["accent"], st["theme"], st["max"], st["cookies"], st["rate"], st["name"]) == ("blue", "dark", 3, "firefox", "2M", "channel"), json.dumps(st))
        check("theme + accent applied, title bar told", await pg.evaluate("document.documentElement.dataset.theme") == "dark" and await pg.evaluate("document.documentElement.dataset.accent") == "blue"
              and ["goTitleBar", True] in await pg.evaluate("window.__calls"))
        await pg.wait_for_timeout(300); await pg.screenshot(path=OUT / "11-settings-blue-dark.png")
        await pg.keyboard.press("Escape"); await pg.wait_for_timeout(500)
        await pg.screenshot(path=OUT / "12-video-blue-dark.png")
        await look(pg, "https://www.youtube.com/watch?v=zzz", VIDEO)
        check("cookies passed to probe", await pg.evaluate("window.__probe.c") == "firefox")
        await pg.evaluate("window.__dl.length=0"); await pg.click("#dlBtn"); await pg.wait_for_timeout(80)
        o = (await pg.evaluate("window.__dl"))[0]["o"]
        check("settings reach download options", (o["cookies"], o["rate"], o["name"]) == ("firefox", "2M", "channel"))
        await pg.evaluate("window.__clip='https://youtu.be/copied123'; window.dispatchEvent(new Event('focus'))"); await pg.wait_for_timeout(100)
        check("copied link offered", await pg.is_visible("#clip") and "youtu.be/copied123" in await pg.inner_text("#clipTxt"))
        await pg.click("#clipUse"); await pg.wait_for_timeout(80)
        check("offer starts a look-up", await pg.evaluate("window.__probe.u") == "https://youtu.be/copied123" and await pg.is_hidden("#clip"))
        check("no page errors (settings flow)", not pg.errs, str(pg.errs))
        await pg.close()

        # ---- 5. no ffmpeg, error, small window ----
        pg = await page_for(b, scheme="dark", ffmpeg=False, size=(760, 560))
        await look(pg, "https://www.youtube.com/watch?v=abc", {**VIDEO, "variants": [{"res": 360, "fps": 30, "size": 41_000_000}]})
        check("no ffmpeg: trim hidden, note shown, extras off", await pg.is_hidden("#trim") and "Without ffmpeg" in await pg.inner_text("#panel") and await pg.is_disabled('[data-tog="embed"]'))
        await pg.click('[data-k="kind"] [data-v="audio"]')
        check("no ffmpeg audio = original", "original audio" in await pg.inner_text("#sum") and await pg.is_disabled('[data-k="audio"] [data-v="mp3"]'))
        await pg.screenshot(path=OUT / "13-noffmpeg-small.png")
        await look(pg, "https://example.com/nope", error="Unsupported URL: https://example.com/nope")
        check("error state", "Couldn't read this link" in await pg.inner_text("#media") and await pg.is_hidden("#panel"))
        await pg.screenshot(path=OUT / "14-error-small.png")
        check("no horizontal scroll at 760px", await pg.evaluate("document.querySelector('#stage').scrollWidth <= document.querySelector('#stage').clientWidth"))
        check("no page errors (edge flow)", not pg.errs, str(pg.errs))
        # the CSP: markup that slipped past escaping must not run, while the app's own script does
        await pg.evaluate("document.querySelector('#toast').innerHTML = '<img src=x onerror=\"window.__xss=1\">'"); await pg.wait_for_timeout(200)
        check("CSP blocks an injected inline handler", await pg.evaluate("window.__xss") is None and any("Content Security Policy" in e for e in pg.errs), str(pg.errs)[:200])
        await b.close()
    print("\nFAILED: " + ", ".join(fails) if fails else "\nall checks passed")
    sys.exit(1 if fails else 0)

asyncio.run(main())
