# Prism on Windows

This guide gets you from a fresh Windows PC to a working Prism. You do not need Docker experience. Follow the steps in order; each one says what you should see.

**Prefer to watch?** The [demo video](prism-demo.mp4) (51 seconds, no sound) shows Prism on Linux; on Windows it looks the same inside the Ubuntu app.

You will need Windows 10 (version 2004 or newer) or Windows 11, and about 4 GB of free disk space.

---

## Step 1: Install Docker Desktop

1. Go to [docker.com/products/docker-desktop](https://www.docker.com/products/docker-desktop/) and download the Windows version.
2. Run the installer. When it asks, leave **Use WSL 2** checked. Accept the other defaults.
3. Restart your computer when the installer asks.
4. After restart, open **Docker Desktop** from the Start menu.
5. Wait until the bottom-left corner says **Engine running**.

You do not need to sign in or create a Docker account.

---

## Step 2: Get a Linux terminal on Windows

Prism runs inside Linux (WSL), which is built into Windows. If you already have Ubuntu or Debian in WSL, skip to step 3.

1. Open **PowerShell as Administrator**: right-click the Start button, pick **Terminal (Admin)**, click **Yes** on the permission prompt.
2. Run:

```powershell
wsl --install -d Ubuntu
```

3. Restart when asked.
4. After restart an **Ubuntu** window opens. Pick a username and password (these are only for Ubuntu, pick anything you will remember). Nothing shows on screen while you type the password; that is normal.

Debian also works if you prefer it (`wsl --install -d Debian`).

---

## Step 3: Connect Docker Desktop to your Linux distro

1. In Docker Desktop, click the **gear icon** (top right) to open Settings.
2. Go to **Resources** then **WSL integration**.
3. Turn on the toggle next to **Ubuntu** (or Debian if that is what you installed).
4. Click **Apply and restart**.

---

## Step 4: Open Ubuntu and check Docker works

1. Open the **Ubuntu** app from the Start menu. You will see a line ending in `$`, like `sam@PC:~$`. That is where you type commands.
2. Install the tools Prism needs:

```bash
sudo apt update && sudo apt install -y curl git
```

3. Check Docker is connected:

```bash
docker ps
```

You should see a header line like `CONTAINER ID   IMAGE   COMMAND ...`. If you see an error instead, go back to step 3. (If it says `docker: command not found`, the toggle in step 3 is off or Docker Desktop is not running. Docker Desktop must be open.)

---

## Step 5: Install and start Prism

Paste this into the Ubuntu window (right-click pastes in Ubuntu):

```bash
curl -fsSL https://raw.githubusercontent.com/sjdsjadjajdjsa/prism/main/install.sh | bash
```

Near the end you should see a line starting with **Done.**

**Now close the Ubuntu window and open a new one** from the Start menu. The installer adds the `prism` command to your terminal's startup file, and only a new window picks it up.

Optional: add free API keys for faster models:

```bash
prism setup
```

Keys are invisible while you paste them; that is normal. Press Enter to skip any key you do not have yet. Prism also works with no keys at all.

Prism will not start in your home folder, so make a practice folder and go into it first:

```bash
mkdir ~/prism-test
cd ~/prism-test
```

Then start Prism:

```bash
prism
```

The first start takes a few minutes while Docker builds Prism. Later starts are quick. When the rainbow PRISM logo appears, press any key and type what you want. Type `/quit` or press `Ctrl + C` to close Prism.

Keep your projects inside Ubuntu (under `~`, like above) rather than in `/mnt/c/...`, which is much slower.

---

## Step 6: Keep Docker Desktop open

Docker Desktop must be running whenever you use Prism. You do not need to do anything in it; just leave it open in the taskbar.

---

## Troubleshooting

### Error mentioning `docker-credential-desktop.exe` and `exec format error`

Linux cannot start Windows programs. This happens when WSL's Windows interop is off (common when `systemd=true` is set in Debian or Ubuntu).

Check whether interop is working:

```bash
cmd.exe /c ver
```

If that fails, run this fix inside Ubuntu:

```bash
sudo mkdir -p /usr/lib/binfmt.d && echo ':WSLInterop:M::MZ::/init:PF' | sudo tee /usr/lib/binfmt.d/WSLInterop.conf
```

Then in PowerShell (not Ubuntu):

```powershell
wsl --shutdown
```

Reopen Ubuntu and try again.

### `docker: command not found` inside Ubuntu

Docker Desktop is not connected to your distro. Go back to step 3 and make sure the toggle for Ubuntu (or Debian) is on, then click **Apply and restart**.

### `prism: command not found`

Close the Ubuntu window and open a new one. If it still does not work, run `~/.prism/prism install`, then open a new Ubuntu window again.

### `Prism won't start in your home folder`

Prism refuses to run in `~` or `/`. Use `cd` to go into a project folder first, for example `mkdir -p ~/prism-test && cd ~/prism-test`.

---

Windows support is new. If you hit an error not listed here, please [open a GitHub issue](https://github.com/sjdsjadjajdjsa/prism/issues) and attach a screenshot of the terminal. That helps a lot.
