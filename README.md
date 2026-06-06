# sportshub

Stream your kid's game straight to GameChanger from any camera, in about 60 seconds.

Plug in a camera, click a button, and the game shows up live on GameChanger for grandma, the away team, and everyone who couldn't make it to the field. That's it. No OBS, no cables to the cloud, no settings to fiddle with.

---

## What you need

1. A **camera** - a Mevo Start, a USB webcam, or any camera your computer can see.
2. A **computer** - Windows, Mac, or Linux (a Raspberry Pi works great at the field!).
3. Your **GameChanger stream link** - GameChanger gives you this when you start a broadcast.

---

## 🚀 Get it

Go to the [**Releases page**](https://github.com/aphexddb/sportshub/releases), download the file for your computer, and unzip it.

- **Windows** → `sportshub_..._windows_amd64.zip`
- **Mac** → `sportshub_..._darwin_arm64.tar.gz` (newer Macs) or `_amd64` (older)
- **Raspberry Pi / Linux** → `sportshub_..._linux_arm64.tar.gz`

> 💡 The **first time** you run it, SportsHub downloads a video helper (ffmpeg) by itself. Just wait a minute — it only happens once.

---

## Stream a game in 5 steps

1. **Double-click `sportshub`** to start it. A little message says it's running.
2. **Open your web browser** and go to:
   - Windows → **http://localhost:8080**
   - Mac / Linux / Pi → **http://localhost**
3. **Plug in your camera** and pick it from the list.
4. **Paste your GameChanger link** into the *"Stream to GameChanger"* box (copy it exactly from GameChanger).
5. **Click `Start GameChanger Stream`.** 🎉 You're live! Check GameChanger — the game is playing.

When the game's over, click **`Stop`**. Done!

---

## Want to watch on the same wifi?

SportsHub also gives you a **low-delay viewer link** (about 1 second behind, way faster than GameChanger). Open the dashboard and click a camera's watch link — perfect for the scorekeeper or the bench.

---

## If something's stuck

| Problem | Try this |
|---|---|
| The page won't open | Make sure `sportshub` is still running. Use the right address (`:8080` on Windows). |
| Camera not showing | Plug it in, then click **Scan / Refresh cameras**. |
| GameChanger says "no stream" | Double-check you pasted the **whole** link from GameChanger, with no spaces. |
| First start is slow | That's the one-time ffmpeg download. Give it a minute. ☕ |

---

## For the tinkerers

Build it yourself (needs [Go](https://go.dev/dl/)):

```bash
make build        # makes ./bin/sportshub
make run          # run it right now
```

Cut a release (builds every platform automatically): `pwsh scripts/release.ps1` — see [scripts/release.ps1](scripts/release.ps1).

---

Made for youth sports families. Go team! 🥎⚽🏀
