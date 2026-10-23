---
name: discord-bot
description: Set up a starter Discord bot in Python with a safe token setup.
---

# Skill: set up a discord.py bot

Use this when the user wants a Discord bot in Python. You build the skeleton; the user runs it.

## Create the bot on Discord first
Explain these steps plainly so the user can follow them:
1. Go to discord.com/developers/applications and click "New Application". Give it a name.
2. Open the "Bot" tab on the left and click "Add Bot".
3. Click "Reset Token" and copy the token. Keep it secret; it is the password to the bot.
4. Under "Privileged Gateway Intents" enable Message Content Intent if the bot reads messages.
5. Open "OAuth2", then "URL Generator". Check the `bot` scope and the permissions you need.
6. Open the generated URL in a browser and invite the bot to a test server.

## Set up the project
- Create a project folder, then a venv: `python3 -m venv .venv` (or fallback `python3 -m virtualenv .venv`).
- Write `requirements.txt` with `discord.py` and `python-dotenv`.
- Install: `.venv/bin/pip install -r requirements.txt`.
- Write `.env` with one line `DISCORD_TOKEN=paste-token-here`. Never put the token in code.
- Write `.gitignore` containing `.env`, `.venv/`, and `__pycache__/`.

## Write the bot
- One file `bot.py`. Load the token with `os.environ` after `load_dotenv()`. Exit with a clear message if missing.
- Use `discord.ext.commands.Bot` with `command_prefix="!"` and `intents.message_content = True`.
- Add a `!ping` command that replies with "pong" and the latency in ms. For a slash command, use `@bot.tree.command(name="ping")` and call `await bot.tree.sync()` in `setup_hook`.
- Add an `on_ready` handler that prints the bot's name so the user knows it connected.
- Guard the runner: require `if __name__ == "__main__": bot.run(TOKEN)`.
- Keep comments short. Add comments only where Discord's API is surprising.

## Verify before you finish
- Run `.venv/bin/python -m py_compile bot.py` to catch syntax errors.
- Run `grep -n "DISCORD_TOKEN" bot.py` to confirm the token is read from the environment, never hardcoded.
- Confirm `.env` is listed in `.gitignore`.
- Never run the bot. It needs the user's real token and stays online as a long-running process.
- The final message must tell the user to enable Message Content Intent and invite the bot with the OAuth2 URL generator, and to run `.venv/bin/python bot.py`.
