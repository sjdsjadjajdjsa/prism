# Setting up Prism, step by step

New to Docker? This guide gets you from nothing to a working Prism. Pick your system and follow the steps in order. Each step says what you should see, so you know it worked.

You'll copy commands into a **terminal**: the window where you type commands.

- **Linux:** search your apps for "Terminal".
- **Mac:** press `Cmd + Space`, type "Terminal", press Enter.
- **Windows:** you'll use the "Ubuntu" app, which step 1 installs.

To paste into a terminal, use `Ctrl + Shift + V` on Linux, `Cmd + V` on Mac, or right-click on Windows.

**Why Docker?** Prism runs inside Docker, which works like a sealed box on your computer. Prism can only see the one project folder you give it, and nothing else. If anything goes wrong, the box gets deleted and your computer stays exactly as it was.

---

## Linux (Ubuntu, Linux Mint, Debian, Pop!_OS)

**1. Install Docker.** It asks for your password. While you type it, nothing shows on screen. That's normal.

```bash
sudo apt update
sudo apt install -y docker.io docker-compose-v2 git curl
```

**2. Let your user use Docker without typing `sudo` every time:**

```bash
sudo usermod -aG docker $USER
```

**3. Log out and log back in**, or restart your computer. Step 2 only takes effect after this.

**4. Check Docker works:**

```bash
docker run hello-world
```

✅ You should see **"Hello from Docker!"**

**5. Install Prism:**

```bash
curl -fsSL https://raw.githubusercontent.com/sjdsjadjajdjsa/prism/main/install.sh | bash
```

✅ Near the end you should see a line starting with **"Done."**

Now jump to [Your first run](#your-first-run).

> Other Linux (Fedora, Arch, …): install Docker with the [official guide](https://docs.docker.com/engine/install/), then follow steps 2 to 5.

---

## Mac

**1. Install Docker Desktop.** It's free for personal use. Go to [docker.com/products/docker-desktop](https://www.docker.com/products/docker-desktop/) and download the Mac version that matches your computer:
- **Apple chip** (M1, M2, M3, M4 and newer): most Macs from 2021 on
- **Intel chip:** older Macs

Not sure which you have? Click the Apple menu  → **About This Mac** and look at "Chip" or "Processor".

**2. Open Docker Desktop** from your Applications folder and accept the terms. You can skip signing in. Wait until the whale icon in the top menu bar stops moving.

**3. Open Terminal and check Docker works:**

```bash
docker run hello-world
```

✅ You should see **"Hello from Docker!"**

**4. Install Prism.** If your Mac asks to install "command line developer tools", click **Install**, wait for it to finish (it can take 10 minutes or more), then run the same command again.

```bash
curl -fsSL https://raw.githubusercontent.com/sjdsjadjajdjsa/prism/main/install.sh | bash
```

✅ Near the end you should see a line starting with **"Done."**

Docker Desktop has to be running whenever you use Prism.

> Mac support is new and hasn't been tested on a real Mac yet. If something breaks, please [open an issue](https://github.com/sjdsjadjajdjsa/prism/issues).

---

## Windows

Prism runs inside **WSL**, which is Linux built into Windows and made by Microsoft. It needs Windows 10 (version 2004 or newer) or Windows 11.

**1. Install WSL.** Right-click the Start button → **Terminal (Admin)** or **PowerShell (Admin)**, then run:

```powershell
wsl --install
```

**2. Restart your computer.** After it restarts, an **Ubuntu** window opens and asks you to pick a username and password. These are only for Ubuntu, so pick anything you'll remember.

If no Ubuntu window opens, open the **Ubuntu** app from the Start menu yourself. If step 1 only printed a list of options instead of installing, run `wsl --install -d Ubuntu` and restart again.

**3. Install Docker Desktop.** It's free for personal use. Go to [docker.com/products/docker-desktop](https://www.docker.com/products/docker-desktop/), download the Windows version and install it with the default options. Open Docker Desktop and wait until it says it's running.

**4. Connect Docker to Ubuntu.** In Docker Desktop, go to **Settings** (the gear icon) → **Resources** → **WSL integration**. Turn on **Ubuntu**, then click **Apply & restart**. If Ubuntu isn't listed, first go to **Settings → General** and tick **Use the WSL 2 based engine**.

**5. Open the Ubuntu app** from the Start menu and check Docker works:

```bash
docker run hello-world
```

✅ You should see **"Hello from Docker!"**

**6. Install Prism (still in the Ubuntu app).** It asks for the Ubuntu password you chose in step 2. Nothing shows while you type.

```bash
sudo apt update && sudo apt install -y git curl
curl -fsSL https://raw.githubusercontent.com/sjdsjadjajdjsa/prism/main/install.sh | bash
```

✅ Near the end you should see a line starting with **"Done."**

Always use Prism from the **Ubuntu** app, not PowerShell, and keep Docker Desktop running.

> Windows support is new and hasn't been tested yet. If something breaks, please [open an issue](https://github.com/sjdsjadjajdjsa/prism/issues).

---

## Your first run

**1. Close the terminal window and open a new one**, so the `prism` command is found.

**2. Make a practice folder and go into it:**

```bash
mkdir ~/prism-test
cd ~/prism-test
```

**3. Start Prism:**

```bash
prism
```

The first time takes a few minutes while Docker builds Prism. Later starts are quick. When the rainbow PRISM logo appears, press any key.

**4. Ask for something.** Type this **inside Prism**, in the box at the bottom, not in a normal terminal:

```
make a python file that asks my name and says hi, then run it
```

**5. Done?** Press `Ctrl + C` to close Prism.

That's it. Prism works with **no keys and no accounts**.

**Optional:** for faster, smarter models, run `prism setup`. It walks you through free keys, about a minute each, and none of them ever ask for a card. You can press Enter to skip any of them.

---

## If something goes wrong

| What you see | What to do |
| --- | --- |
| `permission denied ... docker.sock` | Linux: run `sudo usermod -aG docker $USER`, then log out and back in. |
| `Cannot connect to the Docker daemon` | Mac/Windows: open Docker Desktop and wait until it's running. Linux: `sudo systemctl enable --now docker`. |
| `prism: command not found` | Open a new terminal. Still missing? Run `~/.prism/prism install` (or `./prism install` from the folder you downloaded Prism into). |
| `docker: command not found` (Windows) | In Docker Desktop, turn on WSL integration for Ubuntu (Windows step 4). |
| The first start is very slow | That's normal the first time: Docker downloads and builds everything once. |
| "no free models ready" or "every free AI model is busy" | Check your internet, wait a minute and try again. `prism status` shows each model. Adding a free key with `prism setup` helps a lot. |
| "That folder is too big to share safely" | Prism won't run in your home folder or `/`. Use `cd` to go into a project folder first. |

**Uninstall:** run `prism stop`, then `rm -rf ~/.prism ~/.local/bin/prism`. To also delete Prism's saved chats and memory: `docker volume rm prism_prism-config prism_router-data`.

Still stuck? [Open an issue](https://github.com/sjdsjadjajdjsa/prism/issues) and paste what the terminal showed. Remove any keys first.
