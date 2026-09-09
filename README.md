# Tumblr bot (laptop)

Windows laptop app: log into Tumblr in a dedicated Chrome window, then use the local GUI.

## What you need

- Windows 10 or 11
- [Google Chrome](https://www.google.com/chrome/)
- Git (GitHub Desktop is fine)
- Access to this private repo

`setup.bat` installs [Go](https://go.dev/dl/) with winget if it is missing.

## Get it on the laptop

1. Clone the repo:

   ```bat
   git clone https://github.com/harrieanderson/tumblr.git
   cd tumblr
   ```

2. Double-click **`setup.bat`**. It installs Go if needed, creates folders, and builds `tumblr.exe`.

3. Double-click **`login.bat`**. A **new** Chrome window opens (not your everyday Chrome). Log into Tumblr there, wait until you see the dashboard, then press Enter in the terminal. Your blog name is saved automatically.

4. Double-click **`run.bat`**. The GUI opens at `http://127.0.0.1:8787`.

Leave that terminal open while you use the GUI. Ctrl+C stops it.

## Commands (optional)

From the repo folder, after setup:

```bat
tumblr.exe          rem mixed reach session
tumblr.exe -gui     rem GUI
tumblr.exe -login   rem Chrome login / cookie sync
tumblr.exe -follow  rem reach engage only
tumblr.exe -posts   rem text queue in posts\queue.txt
tumblr.exe -photo   rem photos in pics\
```

If `tumblr.exe` is not there yet:

```bat
go run .
go run . -gui
go run . -login
```

## Files that stay on the laptop

`config/auth.json`, `chrome-data/`, photos, and session data are gitignored. Do not commit them.

Put photos to post in `pics\`. Posted files move to `pics\posted\`. Text posts go in `posts\queue.txt`, one post per line.

## If login fails

- Use the **new** Chrome window from `-login`, not a tab you already had open.
- Close other bot Chrome windows, then run `login.bat` again.
- Confirm Chrome is installed, then re-run `setup.bat`.
